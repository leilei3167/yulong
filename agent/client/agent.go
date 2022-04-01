package client

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"time"
	"yulong-hids/agent/collect"
	"yulong-hids/agent/common"
	"yulong-hids/agent/monitor"

	"github.com/smallnest/rpcx/client"
	"github.com/smallnest/rpcx/share"
)

var err error

type dataInfo struct {
	IP     string              // 客户端的IP地址
	Type   string              // 传输的数据类型
	System string              // 操作系统
	Data   []map[string]string // 数据内容
}

// Agent agent客户端结构
type Agent struct {
	ServerNetLoc string         // 服务端地址 IP:PORT
	Client       client.XClient // RPC 客户端
	ServerList   []string       // 存活服务端集群列表
	PutData      dataInfo       // 要传输的数据
	Reply        int            // RPC Server 响应结果
	Mutex        *sync.Mutex    // 安全操作锁
	IsDebug      bool           // 是否开启debug模式，debug模式打印传输内容和报错信息
	ctx          context.Context
}

var httpClient = &http.Client{
	Timeout:   time.Second * 10,
	Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
}

//TODO:Agent只init一次,是如何做到Server增加后,Agent的Serverlist括容的呢?
//A:refresh函数会定期执行,以更新serverlist的值
func (a *Agent) init() {
	//获取服务器列表
	a.ServerList, err = a.getServerList()
	if err != nil {
		a.log("GetServerList error:", err)
		panic(1)
	}
	//此处设置用于RPCX传递元数据相关的内容:https://doc.rpcx.io/part3/metadata.html
	a.ctx = context.WithValue(context.Background(), share.ReqMetaDataKey, make(map[string]string))
	a.log("Available server node:", a.ServerList)
	if len(a.ServerList) == 0 {
		time.Sleep(time.Second * 30)
		a.log("No server node available")
		panic(1)
	}
	//配置XClient,实现服务发现及认证选项的添加
	a.newClient()
	if common.LocalIP == "" {
		a.log("Can not get local address")
		panic(1)
	}
	a.Mutex = new(sync.Mutex)
	//执行一次RPC调用,将Agent当前主机的信息传到Server,并接收配置信息
	err := a.Client.Call(a.ctx, "GetInfo", &common.ServerInfo, &common.Config)
	if err != nil {
		a.log("RPC Client Call Error:", err.Error())
		panic(1)
	}
	//打印得到的配置信息
	a.log("Common Client Config:", common.Config)
}

// Run 启动agent
func (a *Agent) Run() {

	// agent 初始化
	// 请求Web API，获取Server地址，初始化RPC客户端，获取客户端IP等
	a.init()

	// 每隔一段时间更新初始化配置,会自动更新serverlist的核心代码
	a.configRefresh()

	// 开启各个监控流程 文件监控，网络监控，进程监控,并将监控所得结果通过RPC传输至Server
	a.monitor()

	// 每隔一段时间获取系统信息
	// 监听端口，服务信息，用户信息，开机启动项，计划任务，登录信息，进程列表等
	//阻塞在此
	a.getInfo()
}

//Server动态扩容的关键就在于ServerList,注册服务发现时会将新的服务器列表的KVpair加入,并用随机选择的模式来交互

func (a *Agent) newClient() {
	//注册点对多服务所必须的参数
	var servers []*client.KVPair
	//点对多的注册方式 https://doc.rpcx.io/part2/registry.html#multiple,关注是如何操作KVpair的
	//TODO:Key必须是 tcp@ip:port的格式才能正确调用,serverlist是如何处理的?
	//遍历serverlist,将每个server的ip端口以:分隔,并只取ip忽略端口?

	for _, server := range a.ServerList {
		common.ServerIPList = append(common.ServerIPList, strings.Split(server, ":")[0])
		//只添加Key 忽略value字段
		s := client.KVPair{Key: server}
		servers = append(servers, &s)
		if common.LocalIP == "" {
			a.setLocalIP(server)
			common.ServerInfo = collect.GetComInfo()
			a.log("Host Information:", common.ServerInfo)
		}
	}
	conf := &tls.Config{
		InsecureSkipVerify: true,
	}
	option := client.DefaultOption
	option.TLSConfig = conf
	serverd, _ := client.NewMultipleServersDiscovery(servers)
	a.Client = client.NewXClient("Watcher", FAILMODE, client.RandomSelect, serverd, option)
	a.Client.Auth(AUTH_TOKEN)
}

func (a Agent) getServerList() ([]string, error) {
	var serlist []string
	var url string
	//testmode为true就用http请求
	if TESTMODE {
		url = "http://" + a.ServerNetLoc + SERVER_API
	} else {
		url = "https://" + a.ServerNetLoc + SERVER_API
	}
	a.log("Web API:", url)
	//https://Serverip/json/serverlist
	request, _ := http.NewRequest("GET", url, nil)
	// Close在服务端指定是否在回复请求后关闭连接，在客户端指定是否在发送请求后关闭连接。
	request.Close = true
	//TODO:Server端好像并没有处理http请求的逻辑?serverlist是由哪里写入的?
	resp, err := httpClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	//读取回应
	result, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal([]byte(result), &serlist)
	if err != nil {
		return nil, err
	}
	return serlist, nil
}

func (a Agent) setLocalIP(ip string) {
	conn, err := net.Dial("tcp", ip)
	if err != nil {
		a.log("Net.Dial:", ip)
		a.log("Error:", err)
		panic(1)
	}
	defer conn.Close()
	common.LocalIP = strings.Split(conn.LocalAddr().String(), ":")[0]
}

//定期更新配置等,实现定期更新serverlist的核心代码
func (a *Agent) configRefresh() {
	ticker := time.NewTicker(time.Second * time.Duration(CONFIGR_REF_INTERVAL))
	go func() {
		//每到60s就开启一个协程执行刷新
		for _ = range ticker.C {
			ch := make(chan bool)
			//开启协程进行RPC调用,GetInfo刷新配置信息
			go func() {
				err = a.Client.Call(a.ctx, "GetInfo", &common.ServerInfo, &common.Config)
				if err != nil {
					a.log("RPC Client Call:", err.Error())
					return
				}
				ch <- true
			}()
			// Server集群列表获取
			//监听ch是否调用成功,调用成功则更新list,3秒都没有成功返回ch则终止
			select {
			//如果成功调用到了GetInfo,则执行获取serverlist
			case <-ch:
				serverList, err := a.getServerList()
				if err != nil {
					a.log("RPC Client Call:", err.Error())
					break
				}
				if len(serverList) == 0 {
					a.log("No server node available")
					break
				}
				//如果等于现有的serverlist 的数量,对获取的list进行遍历,如果有某项不同,则替换为serverlist作为最新的
				if len(serverList) == len(a.ServerList) {
					for i, server := range serverList {
						//替换为最新的Serverlist
						if server != a.ServerList[i] {
							a.ServerList = serverList
							// 防止正在传输重置client导致数据丢失
							a.Mutex.Lock()
							//将原有的XClient关闭,以新的serverlist重新创建一个
							a.Client.Close()
							a.newClient()
							a.Mutex.Unlock()
							break
						}
					}
				} else {
					a.log("Server nodes from old to new:", a.ServerList, "->", serverList)
					a.ServerList = serverList
					a.Mutex.Lock()
					a.Client.Close()
					a.newClient()
					a.Mutex.Unlock()
				}
			case <-time.NewTicker(time.Second * 3).C:
				break
			}
		}
	}()
}

func (a *Agent) monitor() {
	//创建channel,用于容纳各协程执行结果
	resultChan := make(chan map[string]string, 16)

	//网络 github.com/akrennmair/gopcap,关于抓包的包
	go monitor.StartNetSniff(resultChan)

	//进程监控,监听当前127.0.0.1:65530;TODO:监听本地的65530端口,如果是其他端口的UDP消息怎么办,UDP为什么能监听Process
	go monitor.StartProcessMonitor(resultChan)

	//文件监控 https://github.com/fsnotify/fsnotify 关于文件系统通知的包
	go monitor.StartFileMonitor(resultChan)

	//获取结果的协程,收集以上三个协程监控获得的数据
	go func(result chan map[string]string) {
		//TODO:服务端有10个安全检测协程,而Agent端只有一个协程来发送,是否合理
		var resultdata []map[string]string
		var data map[string]string
		for {
			//取出数据,并为数据加上当前时间
			data = <-result
			data["time"] = fmt.Sprintf("%d", time.Now().Unix())
			a.log("Monitor data: ", data)
			//将其中source字段对应的值(connection,process等)取提取到source变量,并从data中删除该字段,source即为Type
			source := data["source"]
			delete(data, "source")
			//整个Agent只有一个Agent实例,对于相同的Agent的字段操作需要加锁
			a.Mutex.Lock()
			//设置要发送的PutData字段,data为[]map[string]string切片
			a.PutData = dataInfo{common.LocalIP, source, runtime.GOOS, append(resultdata, data)}
			//异步RPC调用PutInfo方法,Agent传输的DataInfo和Server端所需要的DataInfo结构完全一致
			a.put()
			a.Mutex.Unlock()
		}
	}(resultChan)
}

//TODO:getInfo为什么不做成monitor中的一个子协程,通过chan统一由monitor发送给Server,这样应该就不需要锁?
//会根据当前系统选择执行不同的方法,来获取系统配置项,启动项,任务计划等,若检测到有变化 将发送到Server进行安全检测
func (a *Agent) getInfo() {
	historyCache := make(map[string][]map[string]string)
	for {
		//如果没有MonitorPath则间隔1秒再执行
		if len(common.Config.MonitorPath) == 0 {
			time.Sleep(time.Second)
			a.log("Failed to get the configuration information")
			continue
		}
		//获取所有的信息
		allData := collect.GetAllInfo()
		//是否有修改
		for k, v := range allData {
			if len(v) == 0 || a.mapComparison(v, historyCache[k]) {
				a.log("GetInfo Data:", k, "No change")
				continue
			} else {
				//如果有修改 则发向Server进行检测,为避免与发送网络检测和文件检测产生冲突,需加锁
				a.Mutex.Lock()
				a.PutData = dataInfo{common.LocalIP, k, runtime.GOOS, v}
				a.put()
				a.Mutex.Unlock()
				if k != "service" {
					a.log("Data details:", k, a.PutData)
				}
				historyCache[k] = v
			}
		}
		if common.Config.Cycle == 0 {
			common.Config.Cycle = 1
		}

		time.Sleep(time.Second * time.Duration(common.Config.Cycle) * 60)
	}
}

func (a Agent) put() {
	_, err := a.Client.Go(a.ctx, "PutInfo", &a.PutData, &a.Reply, nil)
	if err != nil {
		a.log("PutInfo error:", err.Error())
	}
}

func (a Agent) mapComparison(new []map[string]string, old []map[string]string) bool {
	if len(new) == len(old) {
		for i, v := range new {
			for k, value := range v {
				if value != old[i][k] {
					return false
				}
			}
		}
		return true
	}
	return false
}

func (a Agent) log(info ...interface{}) {
	if a.IsDebug {
		log.Println(info...)
	}
}
