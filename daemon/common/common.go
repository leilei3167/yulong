package common

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/axgle/mahonia"
	"github.com/kardianos/service"
)

var (
	// M 安全锁
	M *sync.Mutex
	// Cmd agent进程
	Cmd *exec.Cmd
	// Service daemon服务
	Service service.Service
	// ServerIP 服务IP地址
	ServerIP string
	// AgentStatus agent状态，是否启动中
	AgentStatus bool
	// InstallPath agent安装目录
	InstallPath string
	// Arch 系统位数
	Arch string
	// PublicKey 与Server通讯公钥
	PublicKey string
	// HTTPClient httpclient
	HTTPClient *http.Client
	// Proto 请求协议，测试模式为HTTP
	Proto string
)

//初始化安装路径,操作系统位数,初始化客户端,Proto,
func init() {
	M = new(sync.Mutex)
	if TESTMODE {
		Proto = "http"
	} else {
		Proto = "https"
	}
	Arch = "64"
	if runtime.GOOS == "windows" {
		// 不受程序编译位数干扰
		//尝试跳转C:/Windows/SysWOW64,如果为true这为64位
		if _, err := os.Stat(os.Getenv("SystemDrive") + `/Windows/SysWOW64/`); err != nil {
			//Getenv检索并返回名为key的环境变量的值。如果不存在该环境变量会返回空字符串。
			Arch = "32"
		} else {
			Arch = "64"
		}
		//安装路径设置为C:/yulong-hids/
		InstallPath = os.Getenv("SystemDrive") + `/yulong-hids/` //TODO:用"/yulong-hids/" 有啥区别
	} else {
		//如果不是windows操作系统 安装路径设置为:/usr/yulong-hids/
		InstallPath = `/usr/yulong-hids/`
		//执行getconf LONG_BIT将返回系统位数,判断data是否在[]string切片中
		if data, _ := CmdExec("getconf LONG_BIT"); InArray([]string{"32", "64"}, data, false) {
			Arch = data
		}
	}
	//创建客户端:配置Transport
	//要管理代理、TLS配置、keep-alive、压缩和其他设置,必须配置Transport
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true, MaxVersion: 0},
	}
	HTTPClient = &http.Client{
		Transport: transport,
		Timeout:   time.Second * 60,
	}
}

// KillAgent 结束agent
func KillAgent() error {
	if AgentStatus {
		return Cmd.Process.Kill()
	}
	return nil
}

// CmdExec 执行系统命令
func CmdExec(cmd string) (string, error) {
	var c *exec.Cmd
	var data string
	system := runtime.GOOS
	if system == "windows" {
		argArray := strings.Split("/c "+cmd, " ")
		c = exec.Command("cmd", argArray...)
	} else {
		c = exec.Command("/bin/sh", "-c", cmd)
	}
	out, err := c.CombinedOutput()
	if err != nil {
		return data, err
	}
	data = string(out)
	if system == "windows" {
		dec := mahonia.NewDecoder("gbk")
		data = dec.ConvertString(data)
	}
	return data, nil
}

// InArray 判断值是否存在于指定列表中，like为true则为包含判断
func InArray(list []string, value string, like bool) bool {
	for _, v := range list {
		if like {
			if strings.Contains(value, v) {
				return true
			}
		} else {
			if value == v {
				return true
			}
		}
	}
	return false
}

// 获取一个可以绑定的内网IP
func BindAddr() string {
	// 通过连接一个可达的任何一个地址，获取本地的内网的地址
	conn, _ := net.Dial("udp", "114.114.114.114:53")
	defer conn.Close()
	localAddr := conn.LocalAddr().String()
	idx := strings.LastIndex(localAddr, ":")
	return fmt.Sprintf("%s:65512", localAddr[0:idx])
}
