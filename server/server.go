package main

/*Server端实现主要思路:
1.server端不直接与Web端交互,而是通过MongoDB和ES数据库获取Web传入的值(如配置文件,证书,私钥,任务等,通过协程循环获取),在server启动时
将会执行初始化
2.Server通过RPC与Agent和daemon进行交互,tls模式注册有两个服务(GetInfo和PutInfo),分别用于Agent提交自己的主机信息获取agent的配置
以及向Server传入关键的DataInfo,由Server进行分类处理,并存入DB,Server将会把收到的DataInfo放入到ScanChan,交由安全检测线程进行处理

3.Server的核心在于初始化创建的几个线程
	1.心跳线程:维护与数据库的链接,服务注册更新,刷新配置等
	2.任务分发线程,会不断的从DB中试图获取任务(由web指定写入DB),获取到queue之后,根据其包含的IP将提取出的任务发送至指定的Agent,SendTask会对任务
进行加密,通过TCP传输,并获取task的结果存入数据库,此步有并发控制,限制100个协程
	3.安全检测线程:设置有10个协程不断的从ScanChan中获取由Agent通过RPC传入Server的DataInfo(每个协程都是死循环,没有data时会阻塞),会创建Check
结构体来容纳DataInfo的数据,分别按照之前获取的配置选项(黑白名单,规则等)来处理数据,例如:如果在黑名单中 则向客户端发送通知Warning()
	4.客户端健康检测:每30s从数据库读取数据,时间戳间隔一段时间未更新的判定为下线,短期内下线超过20台进行通知;链接检测 每隔一段时间执行连接,离线达到72小时
会将其数据从数据库清除 TODO:主机的初始列表是从哪里获取的? mongoDB,Web是如何监测接入的Agent并存入DB的?
	5.ES的异步写入线程,无限循环监听esChan,PutInfo判断需ES存储的数据会被放入esChan,此协程执行存入
*/
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
