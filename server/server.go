package main

import (
	"context"
	"crypto/tls"
	"errors"
	"io/ioutil"
	"log"
	"time"

	"yulong-hids/server/action"
	"yulong-hids/server/models"
	"yulong-hids/server/safecheck"

	"github.com/smallnest/rpcx/protocol"
	"github.com/smallnest/rpcx/server"
)

const authToken string = "67080fc75bb8ee4a168026e5b21bf6fc"

type Watcher int

// GetInfo agent 提交主机信息获取配置信息
func (w *Watcher) GetInfo(ctx context.Context, info *action.ComputerInfo, result *action.ClientConfig) error {
	//将ComputerInfo存入MongoDB
	action.ComputerInfoSave(*info)
	//根据ComputerInfo的Ip来获取Agent 的信息
	config := action.GetAgentConfig(info.IP)
	log.Println("getconfig:", info.IP)
	*result = config
	return nil
}

// PutInfo 接收处理agent传输的信息
func (w *Watcher) PutInfo(ctx context.Context, datainfo *models.DataInfo, result *int) error {
	//保证数据正常
	if len(datainfo.Data) == 0 {
		return nil
	}
	datainfo.Uptime = time.Now()
	log.Println("putinfo:", datainfo.IP, datainfo.Type)
	//存储信息,根据DataInfo的Type来区分放在es还是MongoDB
	err := action.ResultSave(*datainfo)
	if err != nil {
		log.Println(err)
	}
	//对接收的数据进行统计
	err = action.ResultStat(*datainfo)
	if err != nil {
		log.Println(err)
	}

	//将DataInfo加入待检测队列
	//var ScanChan chan models.DataInfo = make(chan models.DataInfo, 4096)
	//会由安全检测线程取出执行 c.Info = <-ScanChan
	safecheck.ScanChan <- *datainfo
	*result = 1
	return nil
}

//使用到rpcx的认证功能https://doc.rpcx.io/part4/auth.html
//跟预先设定的常量token比较
func auth(ctx context.Context, req *protocol.Message, token string) error {
	if token == authToken {
		return nil
	}
	return errors.New("invalid token")
}

//初始化

func init() {
	log.Println(models.Config)
	// 从数据库获取证书和RSA私钥
	//TODO:这不是在写入吗?Server的配置信息Config从哪来?
	//A:根据引用追溯,会从DB中搜寻配置文件,获取
	ioutil.WriteFile("cert.pem", []byte(models.Config.Cert), 0666)
	//函数向filename指定的文件中写入数据。如果文件不存在将按给出的权限创建文件，否则在写入数据之前清空文件。
	ioutil.WriteFile("private.pem", []byte(models.Config.Private), 0666)

	// 启动心跳线程,无限循环,每隔30s执行检测程序mgoCheck(),regServer(),setConfig(),setRules(),分别对应和DB的连接检测,注册服务,更新配置和规则
	go models.Heartbeat()
	// 启动推送任务线程,创建协程池,从mongo中不断获取queue,一旦有值马上开启sendTask函数,将任务通过TCP推送至目标,将结果储存
	go action.TaskThread()
	// 启动安全检测线程,检测Agent调用PutInfo传入的DataInfo
	go safecheck.ScanMonitorThread()

	// 启动客户端健康检测线程
	go safecheck.HealthCheckThread()
	// ES异步写入线程
	go models.InsertThread()
}
func main() {
	//以tls模式注册服务
	cert, err := tls.LoadX509KeyPair("cert.pem", "private.pem")
	if err != nil {
		log.Println("cert error!")
		return
	}
	config := &tls.Config{Certificates: []tls.Certificate{cert}}
	s := server.NewServer(server.WithTLSConfig(config))
	//添加server的认证函数
	s.AuthFunc = auth
	//rpcx注册服务
	s.RegisterName("Watcher", new(Watcher), "")
	log.Println("RPC Server started")
	err = s.Serve("tcp", ":33433")
	if err != nil {
		log.Println(err.Error())
	}
}
