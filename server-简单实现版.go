package main

import (
	"github.com/gorilla/websocket"
	"log"
	"net/http"
)

var (
	upgrader = websocket.Upgrader{
		// 握手协议中允许跨域,因为很多服务大都是独立部署的,一般都存在跨域问题
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
	}
)

// websocket 处理handler函数
func wsHandler1(w http.ResponseWriter, req *http.Request) {
	var (
		conn *websocket.Conn
		err  error
		//msgType int // 消息类型
		data []byte
	)
	// Upgrade:websocket
	if conn, err = upgrader.Upgrade(w, req, nil); err != nil {
		return
	}
	// ReadMessage是一个使用NextReader获取读者的辅助方法
	// 从该reader读取到缓冲区
	// websocket.Conn
	for {
		// 消息类型有text(json),二进制Binary
		//if msgType,data,err = conn.ReadMessage();err!=nil{
		if _, data, err = conn.ReadMessage(); err != nil {
			goto ERR
		}
		if err = conn.WriteMessage(websocket.TextMessage, data); err != nil {
			goto ERR
		}
	}
	// 标签,出现错误，关闭websocket连接
ERR:
	conn.Close()

	//w.Write([]byte("hello websocket"))
	// 服务端响应给浏览器Upgrade:websocket
	upgrader.Upgrade(w, req, nil)
}

func main() {
	http.HandleFunc("/ws", wsHandler1)
	log.Fatal(http.ListenAndServe(":8080", nil))
}

// websocket url: ws://localhost:8080/ws
// 在线websocket测试工具: http://coolaf.com/tool/chattest
