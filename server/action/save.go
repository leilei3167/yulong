package action

import (
	"strconv"
	"time"
	"yulong-hids/server/models"

	"gopkg.in/mgo.v2/bson"
)

type ComputerInfo struct {
	IP       string
	System   string
	Hostname string
	Type     string
	Path     []string

	Uptime time.Time
}

//ResultSave 保存结果到info表
func ResultSave(datainfo models.DataInfo) error {
	var err error
	// 登录日志、网络连接、进程创建、文件操作 存放在es，其余保存在mongodb
	if datainfo.Type == "loginlog" || datainfo.Type == "connection" || datainfo.Type == "process" || datainfo.Type == "file" {
		if datainfo.Type == "loginlog" {
			//对于登录日志,遍历Data操作数据
			for _, logininfo := range datainfo.Data {
				//更新时间字段key
				time, _ := time.Parse("2006-01-02T15:04:05Z07:00", logininfo["time"])
				//TODO:为什么删除?
				//A:map里面的time删除然后放入到ES的Time字段,考虑到后期对Time单独建立索引便于查询
				delete(logininfo, "time")
				esdata := models.ESSave{
					IP:   datainfo.IP,
					Data: logininfo,
					Time: time,
				}
				models.InsertEs(datainfo.Type, esdata)
			}
		} else {
			//connection,process,file类型的
			//TODO:将转化为int
			//A:上传的datainfo中的时间推断是一个时间戳(从1970年到现在多少秒)
			dataTimeInt, err := strconv.Atoi(datainfo.Data[0]["time"])
			if err != nil {
				return err
			}
			delete(datainfo.Data[0], "time")
			esdata := models.ESSave{
				IP:   datainfo.IP,
				Data: datainfo.Data[0],
				Time: time.Unix(int64(dataTimeInt), 0),
			}
			models.InsertEs(datainfo.Type, esdata)
		}
	} else {
		//其余要放在MongoDB的数据操作
		c := models.DB.C("info")
		count, _ := c.Find(bson.M{"ip": datainfo.IP, "type": datainfo.Type}).Count()
		if count >= 1 {
			err = c.Update(bson.M{"ip": datainfo.IP, "type": datainfo.Type},
				bson.M{"$set": bson.M{"data": datainfo.Data, "uptime": datainfo.Uptime}})
		} else {
			err = c.Insert(&datainfo)
		}
		return err
	}
	return nil
}

// ComputerInfoSave 保存client信息
func ComputerInfoSave(info ComputerInfo) {
	c := models.DB.C("client")
	info.Uptime = time.Now()
	c.Upsert(bson.M{"ip": info.IP}, bson.M{"$set": &info})
	c.Update(bson.M{"ip": info.IP, "$or": []bson.M{bson.M{"health": 1}, bson.M{"health": nil}}}, bson.M{"$set": bson.M{"health": 0}})
}
