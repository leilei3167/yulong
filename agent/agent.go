package main

/*Agent端实现主要思路:
1.通过命令行获取到Server的地址,以及运行模式
2.通过Run函数来开启服务
	2.1 init()初次获取服务列表,并创建RPCX 的CLient客户端,调用一次GetInfo获取Agent的配置
	2.2 configRefresh() 开启协程 每隔60秒调用一次GetInfo获取最新配置信息,如果成功调用GetInfo则会发送请求获取最新的serverlist,获取到list
后会和当前的list对比,有变更则进行替换,并根据serverlist重新创建XClient
	2.3 monitor() 开启数个监控线程,分别监视网络,文件,进程创建,会将监听的结果放入到一个chan,之后统一通过调用PutInfo发到Server进行安全检测
	2.4 getInfo会获取当前的系统各种状态(系统配置项,任务计划,登录日志等),如果检测到有修改,则将数据发到Server进行安全检测

Agent端用了较多我没接触到过的包,系统检测的方法

*/
import (
	"fmt"
	"log"
	"os"
	"runtime"
	"yulong-hids/agent/client"
	"yulong-hids/daemon/common"
)

func main() {
	//必须获取命令行参数
	if len(os.Args) <= 1 {
		fmt.Println("Usage: agent[.exe] ServerIP [debug]")
		fmt.Println("Example: agent 8.8.8.8 debug")
		return
	}
	//如果是linux环境,则执行以下命令

	if runtime.GOOS == "linux" {
		out, _ := common.CmdExec(fmt.Sprintf("lsmod|grep syshook_execve"))
		if out == "" {
			//TODO:不太理解这是执行了什么操作
			common.CmdExec(fmt.Sprintf("insmod %s/syshook_execve.ko", common.InstallPath))
		}
	}
	//新建Agent
	var agent client.Agent
	//Args[0]是程序名称,agent,Args[1]需要用户填写服务器所在IP
	agent.ServerNetLoc = os.Args[1]
	//如果有三个参数并且最后一个是debug,则将Agent开启debug模式
	if len(os.Args) == 3 && os.Args[2] == "debug" {
		log.Println("DEBUG MODE")
		agent.IsDebug = true
	}
	agent.Run()
}
