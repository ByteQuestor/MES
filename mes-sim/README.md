# MES 电池整盘信息仿真系统（mes-sim）

前后端分离的轻量 MES 仿真服务：产线调试/联调时，模拟 MES 向 **WCS**（或任何上位系统）提供“整盘电池信息”查询能力。

- 一盘料 = 一个框（**框体条码**）+ 框内若干物料（每个物料 = **物料条码 + 状态码**）
- 前端界面：可视化新建 / 编辑 / 复制 / 删除料盘，支持**批量生成**、**批量导入**条码
- 对外查询接口：WCS 携带扫码得到的条码（**框体条码或任意一个物料条码**）查询，**均返回整个料盘对象**
- 全部查询自动记入日志（时间 / 条码 / 命中方式 / 来源 IP），便于联调排查

## 为什么部署最快

后端使用 Go 编译为**单个 exe**（约 7MB），前端页面已通过 go:embed 内嵌进二进制：

- 无需安装数据库、JDK、Python、Node 等任何环境
- **拷贝 `mes-sim.exe` 到任意 Windows 电脑，双击即可运行**
- 数据保存在 exe 同目录的 `mes-data.json`，可直接备份 / 迁移 / 手工编辑

## 快速开始

1. 双击 `mes-sim.exe`，浏览器自动打开 `http://localhost:8080`
2. 首次运行如弹出 Windows 防火墙提示，勾选“允许访问”，否则局域网内的 WCS 无法访问
3. 内置示例数据 `BOX-DEMO-0001`（6 颗电池，含 OK/NG/B 混合状态），可直接用于联调；不需要可在界面删除

命令行参数：

| 参数 | 默认 | 说明 |
|---|---|---|
| `-port` | 8080 | HTTP 监听端口（也可用环境变量 `MES_PORT`） |
| `-open` | true | 启动后自动打开浏览器 |
| `-data` | exe目录/mes-data.json | 数据文件路径 |

示例：`mes-sim.exe -port 9000 -open=false`

## WCS 查询接口（核心）

条码可以是**框体条码**或**任意一个物料条码**，均返回整个料盘对象。

### GET

```
GET /api/query?barcode=BOX-DEMO-0001
```

### POST（JSON 或表单均可）

```
POST /api/query
Content-Type: application/json

{"barcode": "BAT-DEMO-0003"}
```

参数名兼容 `barcode` / `barCode` / `code`。

### 响应（HTTP 恒为 200，是否命中看 data.found）

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "found": true,
    "matchType": "material",
    "matchedBarcode": "BAT-DEMO-0003",
    "tray": {
      "id": "17931846444434961008240",
      "boxBarcode": "BOX-DEMO-0001",
      "remark": "示例数据，可在界面删除",
      "materials": [
        { "barcode": "BAT-DEMO-0001", "status": "OK" },
        { "barcode": "BAT-DEMO-0002", "status": "OK" },
        { "barcode": "BAT-DEMO-0003", "status": "NG" }
      ],
      "createdAt": "2026-10-28T18:50:44+08:00",
      "updatedAt": "2026-10-28T18:50:44+08:00"
    }
  }
}
```

说明：

- 未命中：`{"code":0,"message":"ok","data":{"found":false}}`
- `matchType`：`box` = 按框体条码命中，`material` = 按物料条码命中
- `status` 为任意字符串（OK / NG / 0 / 1 / A / B …），由前端自由填写
- 若同一条码出现在多盘，返回**创建最早**的一盘
- 缺少条码参数：HTTP 400 + `{"code":1,"message":"缺少条码参数（barcode）"}`

### curl 自测

```bash
curl "http://localhost:8080/api/query?barcode=BOX-DEMO-0001"
curl -X POST "http://localhost:8080/api/query" -H "Content-Type: application/json" -d "{\"barcode\":\"BOX-DEMO-0001\"}"
```

## 管理接口（前端使用，也可脚本批量造数）

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/trays` | 查询全部料盘 |
| POST | `/api/trays` | 新建料盘 `{"boxBarcode":"...","remark":"...","materials":[{"barcode":"...","status":"OK"}]}` |
| PUT | `/api/trays/{id}` | 更新料盘（全量覆盖） |
| DELETE | `/api/trays/{id}` | 删除料盘 |
| GET | `/api/logs` | 查询日志（最近 500 条，新的在前） |
| DELETE | `/api/logs` | 清空查询日志 |
| GET | `/api/health` | 健康检查 / 服务信息 |

校验规则：框体条码必填且全局唯一；同一盘内物料条码必填且不得重复。

## 数据文件

`mes-data.json`（启动时自动创建，每次变更原子写盘）：

- 直接备份 = 数据备份；拷贝到新电脑 = 数据迁移
- 手工编辑后**重启程序**生效
- 查询日志同样持久化，最多保留 500 条

## 从源码构建

需要 Go 1.21+（或把便携版 Go 解压到 `%USERPROFILE%\.zcode-tools\go`，build.bat 会自动找到）：

```bat
build.bat
```

或手动执行：`go build -trimpath -ldflags "-s -w" -o mes-sim.exe .`

零第三方依赖，纯 Go 标准库，克隆即可编译，无需拉取任何模块。

## 目录结构

```
mes-sim/
├── main.go          # 后端全部代码（API + 存储 + 静态资源服务）
├── web/             # 前端（原生 HTML/CSS/JS，无任何外部依赖，内嵌进 exe）
│   ├── index.html
│   ├── style.css
│   └── app.js
├── build.bat        # 一键构建脚本
├── mes-sim.exe      # 编译产物：拷这一个文件即可部署
└── mes-data.json    # 运行时数据（自动生成）
```
