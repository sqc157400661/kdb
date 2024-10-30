# kdb

#### 介绍
kdb是基于云原生数据库敏捷解决方案，支持MySQL、PostgreSQL等数据库，并支持当前主流的部署架构方案。保护企业使用一整套的方案，方便快捷，易于使用。

#### 软件架构
架构总体分为2层：
1. 控制面
2. 数据面

组件以及职责功能划分：
1. kdb-admin 负责运维管控平台
2. kdb是基于云原生实现的operator控制器，负责k8s集群内实例生命周期的管理
3. kdb-sidecar
    1. kdblet，是负责管控数据库的容器组件，如负责数据库初始化、主动搭建、主从切换等等
    2. kdbmonitor，负责数据库的监控指标的采集
4. KdbProxy，可选组件，是数据库层的proxy
    1. proxysql
    2. pgbouncer
5. Prometheus和grafna 实现的监控报警

MySQL支持的部署架构
- [x] 主备方案（双向复制）
- [x] 一主多从（限制最多3个从节点）
- [ ] MGR架构部署
- [ ] PXC架构部署

#### 安装教程

1.  xxxx
2.  xxxx
3.  xxxx

#### 使用说明

1.  xxxx
2.  xxxx
3.  xxxx

#### 参与贡献

1.  Fork 本仓库
2.  新建 Feat_xxx 分支
3.  提交代码
4.  新建 Pull Request


#### 特技

1.  使用 Readme\_XXX.md 来支持不同的语言，例如 Readme\_en.md, Readme\_zh.md
2.  Gitee 官方博客 [blog.gitee.com](https://blog.gitee.com)
3.  你可以 [https://gitee.com/explore](https://gitee.com/explore) 这个地址来了解 Gitee 上的优秀开源项目
4.  [GVP](https://gitee.com/gvp) 全称是 Gitee 最有价值开源项目，是综合评定出的优秀开源项目
5.  Gitee 官方提供的使用手册 [https://gitee.com/help](https://gitee.com/help)
6.  Gitee 封面人物是一档用来展示 Gitee 会员风采的栏目 [https://gitee.com/gitee-stars/](https://gitee.com/gitee-stars/)
