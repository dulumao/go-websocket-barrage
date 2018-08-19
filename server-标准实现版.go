package main

import (
	"fmt"
	"github.com/gorilla/websocket"
	"net/http"
	"sync"
	"time"
)

// 隐藏细节，封装API
// - 封装Connection结构，隐藏WebSocket底层连接
// - 封装Connection的API,提供Send/Read/Close等线程安全结构
/**
API原理
	SendMessage将消息投递到out channel
	ReadMessage从in channel读取消息
内部原理
	启动读协程，循环读取WebSocket,将消息投递到in channel
	启动写协程，循环读取out channel,讲消息写给WebSocket
*/
// 千万级弹幕系统的秘密
// 3个性能瓶颈：内核瓶颈、锁瓶颈、CPU瓶颈
/**
1. 内核瓶颈：
	推送量大：１００万在线*10条/秒 = 1000万条/秒
	内核瓶颈：　linux内核发送TCP的极限包频率大约是１００万/秒
2. 锁瓶颈：
	需要维护在线用户集合(100万在线)，通常是一个字典结构，一般通过hash字典结构存储长连接
	推送消息即遍历整个集合，顺序发送消息，耗时极长
	推送期间，客户端仍旧正常上下线，所以集合需要上锁
3. CPU瓶颈
	浏览器与服务端通常采取json格式通讯
	json编码非常耗费CPU资源
	向100万在线推送1次，则需要100万次json encode
*/

/**
内核瓶颈－优化原理
	减少网络小包的发送
内核瓶颈－优化方案
	将同一秒内的N条弹幕消息，合并成1条消息
	合并后，每秒推送次数只等于在线连接数

锁瓶颈－优化原理
	大拆小
锁瓶颈－优化方案
	连接打散到多个集合中，每个集合有自己的锁
	多线程并发推送多个集合，避免锁竞争
	读写锁取代互斥锁，多个推送任务可以并发遍历相同集合

CPU瓶颈－优化原理
	减少重复计算
CPU瓶颈－优化方案
	json编码前置，１次消息编码＋100万次推送
	消息合并前置，N条消息合并后只编码１次
*/

/**
单机瓶颈
	维护海量长连接会花费不少内存
	消息推送瞬时消耗大量CPU资源
	消息推送瞬时带宽高达400-600MB(4-6Gbits),是主要瓶颈
*/

// http升级websocket协议的配置
var wsUpgrader = websocket.Upgrader{
	// 允许所有CORS跨域请求
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

// 客户端读写消息
type wsMessage struct {
	messageType int
	data        []byte
}

// 客户端连接
type wsConnection struct {
	wsSocket *websocket.Conn //　底层websocket
	inChan   chan *wsMessage // 读队列
	outChan  chan *wsMessage // 写队列

	mutex     sync.Mutex //避免重复关闭管道
	isClosed  bool
	closeChan chan byte //关闭通知
}

func (wsConn *wsConnection) wsReadLoop() {
	for {
		//读一个message
		msgType, data, err := wsConn.wsSocket.ReadMessage()
		if err != nil {
			goto error
		}
		req := &wsMessage{
			msgType,
			data,
		}
		// 放入请求队列
		select {
		case wsConn.inChan <- req:
		case <-wsConn.closeChan:
			goto closed
		}
	}
error:
	wsConn.wsClose()
closed:
}

func (wsConn *wsConnection) wsWriteLoop() {
	for {
		select {
		//取一个应答
		case msg := <-wsConn.outChan:
			//写给websocket
			err := wsConn.wsSocket.WriteMessage(msg.messageType, msg.data)
			if err != nil {
				goto error
			}
		case <-wsConn.closeChan:
			goto closed
		}
	}
error:
	wsConn.wsClose()
closed:
}

func (wsConn *wsConnection) procLoop() {
	//启动一个goroutine发送心跳
	go func() {
		for {
			time.Sleep(2 * time.Second)
			err := wsConn.wsWrite(websocket.TextMessage, []byte("heartbeat from server"))
			if err != nil {
				fmt.Println("heartbeat fail")
				wsConn.wsClose()
				break
			}
		}
	}()

	// 这是一个同步处理模型(只是一个例子)，如果希望并行处理可以每个请求一个goroutine,注意控制并发goroutine的数量!!!
	for {
		msg, err := wsConn.wsRead()
		if err != nil {
			fmt.Println("read fail")
			break
		}
		fmt.Println(string(msg.data))

		err = wsConn.wsWrite(msg.messageType, msg.data)
		if err != nil {
			fmt.Println("write fail")
			break
		}
	}
}

func wsHandler(w http.ResponseWriter, req *http.Request) {
	// 应答客户端告知升级连接为websocket
	wsSocket, err := wsUpgrader.Upgrade(w, req, nil)
	if err != nil {
		return
	}
	wsConn := &wsConnection{
		wsSocket:  wsSocket,
		inChan:    make(chan *wsMessage, 1000),
		outChan:   make(chan *wsMessage, 1000),
		closeChan: make(chan byte),
		isClosed:  false,
	}

	// 处理器
	go wsConn.procLoop()
	// 读协程
	go wsConn.wsReadLoop()
	//写协程
	go wsConn.wsWriteLoop()
}

func (wsConn *wsConnection) wsRead() (*wsMessage, error) {
	select {
	case msg := <-wsConn.inChan:
		return msg, nil
	case <-wsConn.closeChan:
	}
	//return nil, errors.New("websocket closed")
	return nil, fmt.Errorf("websocket closed")
}

func (wsConn *wsConnection) wsWrite(messageType int, data []byte) error {
	select {
	case wsConn.outChan <- &wsMessage{messageType, data}:
	case <-wsConn.closeChan:
		//return errors.New("websocket closed")
		return fmt.Errorf("websocket closed")
	}
	return nil
}

func (wsConn *wsConnection) wsClose() {
	wsConn.wsSocket.Close()

	wsConn.mutex.Lock()
	defer wsConn.mutex.Unlock()
	if !wsConn.isClosed {
		wsConn.isClosed = true
		close(wsConn.closeChan)
	}
}

func main() {
	http.HandleFunc("/ws", wsHandler)
	http.ListenAndServe(":7777", nil)
}

// websocket url: ws://localhost:8080/ws
// 在线websocket测试工具: http://coolaf.com/tool/chattest

// 获取打开client.html,鼠标右键Run 'client.html'
