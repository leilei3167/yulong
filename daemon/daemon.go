package main

/*daemon逻辑
1.首先会判断命令行所输入的参数判断是安装卸载,如果是安装,则从通过-netlco传入的web 的ip去下载相应的文件,依次安装环境依赖,Agent,下载
agent后会启动daemon.exe指定注册为服务(调用注册逻辑)并启动(net start yulong-hids)
2.开启服务,开启任务接收协程(WaitThread),并运通过命令行Agent.exe(Wait阻塞)
3.daemon开启接收任务的协程,并且启动守护进程的逻辑(执行运行agent的命令,wait阻塞,一旦agent运行终止则执行重启)
4.接收任务协程首先会获取公钥(web的URL直接获取),取得本机出口IP后会固定开启tcp监听65512端口,一旦有链接接入首先会判断其ip是否在serverlist中(URL中获取)
5.对于在serverlist的链接请求,交由tcpPipe处理,tcpPipe会接收Server传过来的任务(map[string]string类型,包含type和command),并执行解码,将结果放入
Task结构体,再执行Task的run方法,根据Type的类型不同执行处理(switch语句,如Kill,quit,update等),是通过common包中的Cmd字段实现对Agent的控制的
(common.Cmd = exec.Command(agentFilePath, common.ServerIP),并将结果进行回传
6.Web和Deamon Agent的直接交互只有获取服务器列表和获取app(更新),甚至任务都是通过存入数据库由Server进行转发的
//TODO:daemon如何退出?

*/
import (
	"flag"
	"log"
	"os"
	"os/exec"
	"runtime"
	"time"

	"yulong-hids/daemon/common"
	"yulong-hids/daemon/install"
	"yulong-hids/daemon/task"

	"github.com/kardianos/service"
)

var (
	ip             *string
	installBool    *bool
	uninstallBool  *bool
	registeredBool *bool
)

//创建服务Service,以及Start run 方法
type program struct{}

//Service接口的Start方法
func (p *program) Start(s service.Service) error {
	go p.run()
	return nil
}

//开启daemon的服务
func (p *program) run() {
	//开启接收任务线程
	go task.WaitThread()
	var agentFilePath string
	if runtime.GOOS == "windows" {
		agentFilePath = common.InstallPath + "agent.exe"
	} else {
		agentFilePath = common.InstallPath + "agent"
	}
	for {
		common.M.Lock()
		log.Println("Start Agent")
		/*Command函数返回一个*Cmd，用于使用给出的参数执行name指定的程序。返回值只设定了Path和Args两个参数。
		如果name不含路径分隔符，将使用LookPath获取完整路径；否则直接使用name。参数arg不应包含命令名。*/
		common.Cmd = exec.Command(agentFilePath, common.ServerIP)
		err := common.Cmd.Start() //开始执行Cmd中包含的命令,但并不会等待该命令完成即返回。Wait方法会返回命令的返回状态码并在命令返回后释放相关的资源。
		common.M.Unlock()
		if err == nil {
			common.AgentStatus = true
			log.Println("Start Agent successful")
			//由于Start运行Agent,如果Agent没有问题,将会一直阻塞到Wait处,所以不存在10秒开启一个Agent
			//此处可以理解为Agent因问题退出,每10s将会执行重启
			err = common.Cmd.Wait() //Wait会阻塞直到该命令执行完成，该命令必须是被Start方法开始执行的。
			if err != nil {
				common.AgentStatus = false
				log.Println("Agent to exit：", err.Error())
			}
		} else {
			log.Println("Startup Agent failed", err.Error())
		}
		time.Sleep(time.Second * 10)
	}
}

func (p *program) Stop(s service.Service) error {
	common.KillAgent()
	return nil
}

func main() {
	//定义命令行参数
	//将通过 -netloc获取的值赋值给ServerIP,注意要是web服务的IP

	flag.StringVar(&common.ServerIP, "netloc", "", "* WebServer 192.168.1.100:443")
	//指定-install 未指定就是false
	installBool = flag.Bool("install", false, "Install yulong-hids service")
	uninstallBool = flag.Bool("uninstall", false, "Remove yulong-hids service")
	registeredBool = flag.Bool("register", false, "Registration yulong-hids service")
	flag.Parse()
	//TODO:不熟悉的包
	//github.com/kardianos/service 包,用于创建系统服务
	//service will install / un-install, start / stop, and run a program as a service (daemon).
	svcConfig := &service.Config{
		Name:        "yulong-hids",
		DisplayName: "yulong-hids",
		Description: "集实时监控、异常检测、集中管理为一体的主机安全监测系统",
		Arguments:   []string{"-netloc", common.ServerIP}, //从命令行获取的ServerIP
	}
	//生成daemon服务,TODO:将prg相应的方法注册给Service?
	prg := &program{}
	var err error
	//创建新的Service
	common.Service, err = service.New(prg, svcConfig)
	if err != nil {
		log.Println("New a service error:", err.Error())
		return
	}
	//判断命令行参数是否指定了uninstall,如果是则执行卸载逻辑
	if *uninstallBool {
		task.UnInstallALL()
		return
	}
	//如果没有指定任何的参数,则打印所有帮助信息并终止程序
	if len(os.Args) <= 1 {
		flag.PrintDefaults()
		return
	}

	//如果指定了 -install 则进行安装逻辑
	// 释放agent
	if *installBool {
		// 依赖环境安装
		if _, err = os.Stat(common.InstallPath); err != nil { //Stat返回一个描述给定name的FileInfo,如果是一个符号链接则尝试跳转
			//否则创建安装路径的文件夹
			os.Mkdir(common.InstallPath, 0)
			//通过Web的IP,和安装路径,操作系统位数来安装
			err = install.Dependency(common.ServerIP, common.InstallPath, common.Arch)
			if err != nil {
				log.Println("Install dependency, service error:", err.Error())
				return
			}
		}

		if common.ServerIP == "" {
			flag.PrintDefaults()
			return
		}
		//安装Agent
		err := install.Agent(common.ServerIP, common.InstallPath, common.Arch)
		if err != nil {
			log.Println("Install agent error:", err.Error())
			return
		}
		log.Println("Installed!")
		return
	}
	// 安装daemon为服务(由安装Agent调用,并且Agent会执行net start yullong-hids命令直接启动服务)
	if *registeredBool {
		//安装服务
		err = common.Service.Install()
		if err != nil {
			log.Println("Install daemon as service error:", err.Error())
		} else {
			//开启服务
			if err = common.Service.Start(); err != nil {
				log.Println("Service start error:", err.Error())
			} else {
				log.Println("Install as a service", "ok")
			}
		}
		return
	}
	//运行服务 TODO:在安装 Agent时就已经net start yulong-hids了 和这一步是否有冲突?
	err = common.Service.Run()
	if err != nil {
		log.Println("Service run error:", err.Error())
	}
}
