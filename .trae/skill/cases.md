### 一、前置准备

```plain
创建软链
ln -s /home/cib/agent/metric-agent-amd64 agent
指定端口启动agent
nohup /home/cib/agent/metric-agent-amd64 --config=/home/cib/agent/metricAgent.yml --bind-addr=0.0.0.0:9092 --logs=/home/cib/agent/logs/metricAgent.log > /home/cib/agent/logs/metricAgent.log 2>&1 &
不指定端口启动agent
nohup /home/cib/agent/metric-agent-amd64 --config=/home/cib/agent/metricAgent.yml   --logs=/home/cib/agent/logs/metricAgent.log > /home/cib/agent/logs/metricAgent.log 2>&1 &
```


<br />

<br />

### 二、指令模式

#### 1、不设置端口执行脚本

```plain
[cib@localhost agent]$ ./agent -exec 'ps -ef|grep java'
cib       1887     1  3 10:00 pts/0    00:01:58 /usr/local/java/jdk1.8.0_44/bin/java -Djava.ext.dirs=/usr/local/java/jdk1.8.0_44/jre/lib/ext:/usr/local/java/jdk1.8.0_44/lib/ext -Xms512m -Xmx512m -Xmn256m -Dnacos.standalone=true -Dnacos.member.list= -XX:+UseConcMarkSweepGC -XX:+UseCMSCompactAtFullCollection -XX:CMSInitiatingOccupancyFraction=70 -XX:+CMSParallelRemarkEnabled -XX:SoftRefLRUPolicyMSPerMB=0 -XX:+CMSClassUnloadingEnabled -XX:SurvivorRatio=8 -Xloggc:/home/cib/nacos/logs/nacos_gc.log -verbose:gc -XX:+PrintGCDetails -XX:+PrintGCDateStamps -XX:+PrintGCTimeStamps -XX:+UseGCLogFileRotation -XX:NumberOfGCLogFiles=10 -XX:GCLogFileSize=100M -Dloader.path=/home/cib/nacos/plugins,/home/cib/nacos/plugins/health,/home/cib/nacos/plugins/cmdb,/home/cib/nacos/plugins/selector -Dnacos.home=/home/cib/nacos -jar /home/cib/nacos/target/nacos-server.jar --spring.config.additional-location=file:/home/cib/nacos/conf/ --logging.config=/home/cib/nacos/conf/nacos-logback.xml --server.max-http-header-size=524288 nacos.nacos
cib       2630  2329  0 11:00 pts/1    00:00:00 ./agent -exec ps -ef|grep java
cib       2634  2577  0 11:00 pts/1    00:00:00 bash -c ps -ef|grep java
cib       2636  2634  0 11:00 pts/1    00:00:00 grep java
```


#### 2、设置端口执行脚本

```plain
[cib@localhost agent]$ ./agent -exec 'ps -ef|grep java' --port 8081
cib       1887     1  3 10:00 pts/0    00:02:05 /usr/local/java/jdk1.8.0_44/bin/java -Djava.ext.dirs=/usr/local/java/jdk1.8.0_44/jre/lib/ext:/usr/local/java/jdk1.8.0_44/lib/ext -Xms512m -Xmx512m -Xmn256m -Dnacos.standalone=true -Dnacos.member.list= -XX:+UseConcMarkSweepGC -XX:+UseCMSCompactAtFullCollection -XX:CMSInitiatingOccupancyFraction=70 -XX:+CMSParallelRemarkEnabled -XX:SoftRefLRUPolicyMSPerMB=0 -XX:+CMSClassUnloadingEnabled -XX:SurvivorRatio=8 -Xloggc:/home/cib/nacos/logs/nacos_gc.log -verbose:gc -XX:+PrintGCDetails -XX:+PrintGCDateStamps -XX:+PrintGCTimeStamps -XX:+UseGCLogFileRotation -XX:NumberOfGCLogFiles=10 -XX:GCLogFileSize=100M -Dloader.path=/home/cib/nacos/plugins,/home/cib/nacos/plugins/health,/home/cib/nacos/plugins/cmdb,/home/cib/nacos/plugins/selector -Dnacos.home=/home/cib/nacos -jar /home/cib/nacos/target/nacos-server.jar --spring.config.additional-location=file:/home/cib/nacos/conf/ --logging.config=/home/cib/nacos/conf/nacos-logback.xml --server.max-http-header-size=524288 nacos.nacos
cib       2804  2329  0 11:07 pts/1    00:00:00 ./agent -exec ps -ef|grep java --port 8081
cib       2808  2797  0 11:07 pts/1    00:00:00 bash -c ps -ef|grep java
cib       2811  2808  0 11:07 pts/1    00:00:00 grep java

```


#### 3、不设置端口执行脚本文件

```plain
[cib@localhost agent]$ ./agent -exec-file ./test.sh
agent
cache
crontab.yml
data
logs
metric-agent-amd64
metric-agent-arm64
metricAgent.yml
metricFileConfig_k8s.xxx11
metricFileConfig_k8s.xxx11_agent_bak
scheduledConfig.yml
test.sh
```


#### 4、设置端口执行脚本文件

```plain
[cib@localhost agent]$  ./agent -exec-file ./test.sh --port 8081
agent
cache
crontab.yml
data
logs
metric-agent-amd64
metric-agent-arm64
metricAgent.yml
metricFileConfig_k8s.xxx11
metricFileConfig_k8s.xxx11_agent_bak
scheduledConfig.yml
test.sh

```


#### 5、端口不匹配报错

```plain
[cib@localhost agent]$ ./agent -exec-file ./test.sh --port 8082
错误：无法连接到本地 MetricAgent 服务 (127.0.0.1:8082)：Post "http://127.0.0.1:8082/api/v1/exec": dial tcp 127.0.0.1:8082: connect: connection refused
请先启动 MetricAgent HTTP 服务或确认端口后再执行指令模式


```


<br />

#### 6、文件不存在报错

```plain
[cib@localhost agent]$ ./agent -exec-file ./test1.sh --port 8081
错误：读取脚本文件失败 open /home/cib/agent/test1.sh: no such file or directory


```


<br />

#### 7、文件不存在报错

```plain
[cib@localhost agent]$ ./agent -exec 'p22s -ef|grep java' --port 8081
bash: p22s: 未找到命令

```


<br />

### 三、HTTP模式

#### 1、健康检查

```plain
http://192.168.50.177:8081/health
{"status":"ok","agent_id":"node-001","agent_group":"k8s","timestamp":1788405548}

```


<br />

#### 2、远程命令执行

```plain
http://192.168.50.177:8081/api/v1/exec
{
  "script": "ls",
  "timeout": 30
}
{
  "script": "./test.sh",
  "timeout": 30
}


```


<br />

#### 3、请求透传

```plain
http://127.0.0.1:9092/forward?target=http://127.0.0.1:9092/health
{
    "status": "ok",
    "agent_id": "node-001",
    "agent_group": "k8s",
    "timestamp": 1788418112
}

```


<br />

#### 4、文件上传

```plain
http://192.168.50.177:8081/api/v1/upload
{
    "code": 0,
    "data": {
        "fileName": "TODO.txt",
        "size": 628,
        "targetPath": "/home/cib/agent/TODO.txt"
    },
    "message": "ok"
}


```


<br />

<br />

### 四、后台任务

#### 1、配置分发

```plain


```


<br />

<br />

#### 2、定时任务

```plain


```


<br />

<br />

#### 3、守护进程

```plain


```


<br />

