package action

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"log"
	"net"
	"time"
	"yulong-hids/server/models"

	mgo "gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"
)

type queue struct {
	ID      bson.ObjectId `bson:"_id"`
	TaskID  bson.ObjectId `bson:"task_id"`
	IP      string        `bson:"ip"`
	Type    string        `bson:"type"`
	Command string        `bson:"command"`
	Time    time.Time     `bson:"time"`
}
type taskResult struct {
	TaskID bson.ObjectId `bson:"task_id"`
	IP     string        `bson:"ip"`
	Status string        `bson:"status" json:"status"`
	Data   string        `bson:"data" json:"data"`
	Time   time.Time     `bson:"time"`
}

var threadpool chan bool

// TaskThread 开启任务线程
func TaskThread() {
	log.Println("Start Task Thread")
	//目的是限制goroutine数量
	threadpool = make(chan bool, 100)

	for {
		//创建任务队列实例
		res := queue{}
		change := mgo.Change{
			Remove: true,
		}
		//查找队列是否有值,没有的话休眠10秒再次查询
		//TODO:queue是由谁放入的?
		//A:web由用户插入
		models.DB.C("queue").Find(bson.M{}).Limit(1).Apply(change, &res)
		if res.IP == "" {
			time.Sleep(time.Second * 10)
			continue
		}
		//如果queue有结果,则向线程池放入一个true并开启协程传入queue实例进行处理
		threadpool <- true
		go sendTask(res, threadpool)
	}
}

//统一处理错误信息,打印并存入mongo
func saveError(task queue, errMsg string) {
	log.Println(errMsg)
	res := taskResult{task.ID, task.IP, "false", errMsg, time.Now()}
	c := models.DB.C("task_result")
	err := c.Insert(&res)
	if err != nil {
		log.Println(err.Error())
	}
}

//处理任务 TODO:这里只是将Task编码并传递到某个位置,为进行处理,那么是在哪里进行处理的?
func sendTask(task queue, threadpool chan bool) {
	//结束一个任务时 腾出一个线程池空间
	defer func() {
		<-threadpool
	}()
	//提取Type和Command
	sendData := map[string]string{"type": task.Type, "command": task.Command}
	//将其序列化,没有错误的话,连接到task.IP的65512端口,TODO:连接到哪里?
	//A:根据全文搜索,将其发到了daemon的65512的端口
	if data, err := json.Marshal(sendData); err == nil {
		conn, err := net.DialTimeout("tcp", task.IP+":65512", time.Second*3)
		log.Println("sendtask:", task.IP, sendData)
		if err != nil {
			saveError(task, err.Error())
			return
		}
		defer conn.Close()
		//将序列化为json的sendData,TODO:如何加密?
		//A:通过将web读取出的证书和私钥(读取出来是string,需转回成真正的密钥才能使用)进行加密
		encryptData, err := rsaEncrypt(data)
		if err != nil {
			saveError(task, err.Error())
			return
		}
		//TODO:为什么要再进行编码成base64,再传输(还强转回[]byte)
		//A:编码,非必须
		conn.Write([]byte(base64.RawStdEncoding.EncodeToString(encryptData) + "\n"))
		//创建缓冲Reader,读取conn的数据,直到读到'\n'
		reader := bufio.NewReader(conn)
		msg, err := reader.ReadString('\n')
		if err != nil || len(msg) == 0 {
			saveError(task, err.Error())
			return
		}
		//记录发送信息方的IP以及消息
		log.Println(conn.RemoteAddr().String(), msg)
		//创建结果,并执行转码,获取结果 TODO:将Type和Command传到指定IP的65512端口(daemon),由其处理后返回taskResult
		res := taskResult{}
		err = json.Unmarshal([]byte(msg), &res)
		if err != nil {
			saveError(task, err.Error())
			return
		}
		//结果存入数据库
		res.TaskID = task.TaskID
		res.Time = time.Now()
		res.IP = task.IP
		c := models.DB.C("task_result")
		err = c.Insert(&res)
		if err != nil {
			saveError(task, err.Error())
			return
		}
	}
}
