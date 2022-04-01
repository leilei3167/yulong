package main

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

type program struct{}

func (p *program) Start(s service.Service) error {
	go p.run()
	return nil
}

func (p *program) run() {
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
		common.Cmd = exec.Command(agentFilePath, common.ServerIP)
		err := common.Cmd.Start()
		common.M.Unlock()
		if err == nil {
			common.AgentStatus = true
			log.Println("Start Agent successful")
			err = common.Cmd.Wait()
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
	//生成daemon服务
	prg := &program{}
	var err error
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
		err := install.Agent(common.ServerIP, common.InstallPath, common.Arch)
		if err != nil {
			log.Println("Install agent error:", err.Error())
			return
		}
		log.Println("Installed!")
		return
	}
	// 安装daemon为服务
	if *registeredBool {
		err = common.Service.Install()
		if err != nil {
			log.Println("Install daemon as service error:", err.Error())
		} else {
			if err = common.Service.Start(); err != nil {
				log.Println("Service start error:", err.Error())
			} else {
				log.Println("Install as a service", "ok")
			}
		}
		return
	}
	err = common.Service.Run()
	if err != nil {
		log.Println("Service run error:", err.Error())
	}
}
