package main

import (
	_ "yulong-hids/web/routers"
	"yulong-hids/web/settings"
	"yulong-hids/web/utils"

	"github.com/astaxie/beego"
)

var (
	logFile         string
	logConfJSON     string
	defaultLogLevel int
)

func init() {
	defaultLogLevel = beego.LevelInformational //6
	logFile = beego.AppConfig.String("logfile")
	logConfJSON = `{"filename":"` + logFile + `"}`
}

func main() {
	beego.SetLogger("file", logConfJSON)
	//设置session
	beego.BConfig.WebConfig.Session.SessionGCMaxLifetime = settings.SessionGCMaxLifetime
	//项目所在的目录 TODO:path包获取目录地址
	settings.ProjectPath = utils.GetCwd()
	settings.FilePath = utils.DloadFilePath(settings.ProjectPath)

	// set loglevel
	//加载配置文件 判断runmode字段,以设置全局日志的级别
	if beego.AppConfig.String("runmode") == "dev" {
		beego.SetLevel(beego.LevelDebug)
	} else if level, err := beego.AppConfig.Int("loglevel"); err == nil {
		beego.SetLevel(level)
	} else {
		beego.SetLevel(defaultLogLevel)
	}

	// add /tests to https://domain/tests as static path in develop mode
	if utils.IsDevMode() {
		beego.SetStaticPath("/tests", "tests")
	}

	beego.Run()
}
