package task

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"strings"
	"yulong-hids/daemon/common"
)

type taskServer struct {
	TCPListener net.Listener
	ServerIP    string
	ServerList  []string
}

func (t *taskServer) listen() (err error) {
	t.TCPListener, err = net.Listen("tcp", common.BindAddr())
	return err
}

func (t *taskServer) run() {
	//获取TCPListener字段,获取本机出口IP 固定监听65512端口
	err := t.listen()
	if err != nil {
		return
	}
	log.Println("Start the task listener thread")
	//开启监听 出口IP
	for {
		tcpConn, err := t.TCPListener.Accept()
		if err != nil {
			fmt.Println("Accept new TCP listener error:", err.Error())
			continue
		}
		//获取ServerIP,任务由Web存在数据库,由Server获取后发送给指定IP
		t.ServerIP = strings.SplitN(tcpConn.RemoteAddr().String(), ":", 2)[0]
		//获取到IP之后 需判断是否是在Serverlist 中 ,否则断开连接
		if t.isServer() {
			t.tcpPipe(tcpConn)
		} else {
			tcpConn.Close()
		}
	}
}
func (t *taskServer) isServer() bool {
	//TODO:server端没有注册http相关的服务 为什么能获取list
	t.setServerList()
	for _, ip := range t.ServerList {
		//用:分割ip,最终只返回2个元素
		if t.ServerIP == strings.SplitN(ip, ":", 2)[0] {
			return true
		}
	}
	return false
}
func (t *taskServer) setServerList() error {
	resp, err := common.HTTPClient.Get(common.Proto + "://" + common.ServerIP + common.SERVER_LIST_API)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	result, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	json.Unmarshal([]byte(result), &t.ServerList)
	return nil
}

func (t *taskServer) tcpPipe(conn net.Conn) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	message, err := reader.ReadBytes('\n')
	if err != nil {
		return
	}
	decodeBytes, _ := base64.RawStdEncoding.DecodeString(string(message))
	decryptdata, err := rsaDecrypt(decodeBytes)
	if err != nil {
		log.Println("Decrypt rsa text in tcpPipe error:", err.Error())
		return
	}
	var taskData map[string]string
	err = json.Unmarshal(decryptdata, &taskData)
	if err != nil {
		log.Println("Unmarshal json text in tcpPipe error", err.Error())
		return
	}
	var taskType string
	var data string
	if _, ok := taskData["type"]; ok {
		taskType = taskData["type"]
	}
	if _, ok := taskData["command"]; ok {
		data = taskData["command"]
	}
	result := map[string]string{"status": "false", "data": ""}
	T := Task{taskType, data, result}
	if sendResult := T.Run(); len(sendResult) != 0 {
		conn.Write(sendResult)
	}
}

// WaitThread 接收任务线程
func WaitThread() {
	//从web获取到公钥
	setPublicKey()
	var t taskServer
	t.run()
}
