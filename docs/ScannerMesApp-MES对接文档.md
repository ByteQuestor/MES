# ScannerMesApp ↔ MES 接口对接文档

> 版本：v1.0（2026-10-28）
> 适用：BytequestorPlus 框架 · ScannerMesApp 盘料核验工作站 ↔ MES 整盘信息查询接口（当前对端为 `mes-sim` 仿真服务，正式 MES 按同一契约接入）

---

## 1. 背景与范围

ScannerMesApp 是部署在扫码工位的 WinForms 工作站：操作工扫一个条码（框体条码或物料条码），程序携带该条码调用 MES 查询**整盘（整框）判定**，并把每个通道（每颗物料）的状态渲染在盘面网格上。

本文档覆盖：

- 与 MES 的通信契约（请求 / 响应 / 错误语义）
- ScannerMesApp 侧 HTTP 客户端的行为与映射规则
- 客户端配置项与联调步骤
- 接入正式 MES 前需要与 MES 供应商确认的问题清单

**不在本文档范围**：扫码器硬件接入（TCP 直连读码器 / S7-1200 PLC 读条码），该部分见 `ScannerMesApp\现场验证说明.md`。

## 2. 系统现状梳理（对接前）

### 2.1 相关代码结构

| 位置 | 内容 | 状态 |
|---|---|---|
| `BytequestorPlus\Core\Mes\Abstraction\IMesInspectionClient.cs` | MES 查询抽象：`QueryTrayAsync(barcode, product, ct)`，返回 `OperationResult<MesTrayResult>` | 已有，本次不变 |
| `BytequestorPlus\Core\Mes\Entity\MesTrayResult.cs` | 盘级结果 `MesTrayResult(OverallStatus, Message, Channels[], RawPayload)` + 通道结果 `MesChannelResult(ChannelNumber, StatusCode, DisplayText)` | 已有，本次不变 |
| `BytequestorPlus\Core\Mes\Entity\MesApiSettings.cs` | MES 接入配置（新增，原在 ScannerMesApp 内） | **本次新增/上移** |
| `BytequestorPlus\Infrastructure\Mes\SimulatedMesInspectionClient.cs` | 演示客户端：按条码哈希生成伪判定，不发网络请求 | 已有，保留为演示模式 |
| `BytequestorPlus\Infrastructure\Mes\HttpMesInspectionClient.cs` | 真实 HTTP 客户端 | **本次新增** |
| `ScannerMesApp\InspectionForm.cs` | 工作站主界面：扫码 → 查询 MES → 盘面渲染 | 小幅改造（换客户端、文案、超时提示） |
| `ScannerMesApp\MesApiSettingsForm.cs` | MES 接口设置对话框 | **本次改造**（新增演示模式开关、测试请求按钮） |
| `ScannerMesApp\scanner-app.json` | 应用配置（扫码器 / 产品 / MesApi） | MesApi 默认值更新 |

### 2.2 对接前的问题

1. MES 侧只有 `SimulatedMesInspectionClient`：结果由条码字符串哈希决定，与真实 MES 无关。
2. `MesApiSettingsForm` 保存的 URL / 请求方式 / 超时只是落盘，**不产生任何网络请求**（界面明示"配置不会发起真实请求"）。
3. 默认配置指向 `http://127.0.0.1:5000/api/inspection/tray`，没有对应服务。

本次改造后：`HttpMesInspectionClient` 按 `scanner-app.json` 的 MesApi 配置真实调用 MES；演示模式由显式开关（`UseSimulation`）控制，用于无 MES 环境的离线演示。

## 3. 总体链路

```
┌────────────────────────── ScannerMesApp（工位 PC）──────────────────────────┐
│                                                                            │
│  扫码来源（硬件：TCP/PLC；或键盘/模拟扫码）                                   │
│        │  ScanResult{Barcode, ScanTime}                                    │
│        ▼                                                                   │
│  InspectionForm.QueryMesAsync(barcode)                                     │
│        │  busy 防重入：上一次查询未完成时忽略新扫码                          │
│        ▼                                                                   │
│  IMesInspectionClient.QueryTrayAsync(barcode, product, ct)                 │
│    ├─ UseSimulation=true  → SimulatedMesInspectionClient（本地伪数据）      │
│    └─ UseSimulation=false → HttpMesInspectionClient                        │
│              │  GET  {url}?barcode={条码}                                  │
│              │   或 POST {url}  body={"barcode":"{条码}"}                   │
│              ▼                                                             │
└──────────────┼─────────────────────────────────────────────────────────────┘
               │ HTTP（超时 = MesApi.TimeoutSeconds）
               ▼
        MES 服务（当前 = mes-sim）
        GET/POST /api/query → 整盘对象
```

## 4. 通信契约（以 mes-sim 为基准）

### 4.1 请求

| 项 | 约定 |
|---|---|
| 路径 | 由 `MesApi.RequestUrl` 完整给出（如 `http://127.0.0.1:8080/api/query`） |
| 方法 | `GET` 或 `POST`（`MesApi.Method` 配置） |
| 条码参数名 | `barcode`（对端兼容 `barCode` / `code`，客户端固定发 `barcode`） |
| 编码 | UTF-8；条码做 URL 转义（GET）或 JSON 字符串（POST） |
| Content-Type | POST 时 `application/json` |
| 超时 | `MesApi.TimeoutSeconds`（1–120 秒），超时与窗体取消共用一个取消令牌 |

GET：

```
GET /api/query?barcode=BOX-DEMO-0001
```

POST：

```
POST /api/query
Content-Type: application/json

{"barcode":"BOX-DEMO-0001"}
```

> 扩展约定：后续若 MES 需要产品号/工单等字段，在 POST JSON 体上增加字段（如 `productCode`），GET 继续只带 `barcode`；MES 端必须容忍未知字段。

### 4.2 响应（HTTP 恒为 200，业务结果看 `data.found`；仅参数缺失/方法错误才返回 4xx/5xx）

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "found": true,
    "matchType": "box",
    "matchedBarcode": "BOX-DEMO-0001",
    "tray": {
      "id": "17931846444434961008240",
      "boxBarcode": "BOX-DEMO-0001",
      "remark": "示例数据",
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

| 字段 | 类型 | 说明 |
|---|---|---|
| `code` | int | 0 = 业务成功；非 0 = 业务失败，`message` 给原因 |
| `message` | string | 提示信息 |
| `data.found` | bool | 条码是否命中某盘料 |
| `data.matchType` | string | `box`=按框体条码命中；`material`=按物料条码命中 |
| `data.matchedBarcode` | string | 命中的条码 |
| `data.tray.boxBarcode` | string | 框体条码 |
| `data.tray.materials[]` | array | 盘内物料：`barcode` 物料条码、`status` 状态字符串（`OK`/`NG`/任意值） |
| `data.tray.remark` 等 | - | 其余字段原样保留在 `MesTrayResult.RawPayload` |

未命中：`{"code":0,"message":"ok","data":{"found":false}}`
缺参数：HTTP 400 + `{"code":1,"message":"缺少条码参数（barcode）"}`

## 5. ScannerMesApp 侧映射规则（HttpMesInspectionClient）

客户端把 MES 响应转换为界面使用的 `MesTrayResult`，规则如下：

| 输入 | 客户端行为 | 界面表现 |
|---|---|---|
| 条码为空 | `Failure("条码不能为空")` | 红色"查询失败" |
| 网络不通 / HTTP 异常 | `Failure("无法连接 MES 服务：<原因>")` | 红色"查询失败" |
| HTTP 状态码非 2xx | `Failure("MES 返回 HTTP <状态码>：<响应体前 200 字>")` | 红色"查询失败" |
| 响应不是合法 JSON | `Failure("MES 响应不是合法 JSON：<响应体前 200 字>")` | 红色"查询失败" |
| `code != 0` | `Failure(message)` | 红色"查询失败" |
| `found = false` | `Failure("条码 <X> 未在 MES 中登记（未命中任何料盘）")` | 红色"查询失败" |
| `found = true` 但 `tray` 缺失 | `Failure("MES 返回缺少料盘数据")` | 红色"查询失败" |
| `found = true` 且有 `tray` | 映射为 `MesTrayResult`（见下） | 盘面渲染 |

**成功映射规则：**

1. **通道编号 = 物料在 `materials[]` 数组中的位置 + 1**（第 1 颗 → 通道 1，第 2 颗 → 通道 2 …）。当前 mes-sim 契约不含显式通道号；正式 MES 若提供通道号，仅需修改本客户端的映射一处。
2. `MesChannelResult.StatusCode = materials[i].status`（原样字符串，界面按 OK/NG/复测/其他着色）；`DisplayText = materials[i].barcode`（盘面格子里显示物料条码）。
3. **盘级状态 `OverallStatus` 推导**：
   - 任一物料 NG（`NG/FAIL/BAD/不良/不合格`）→ `NG`
   - 全部 OK（`OK/PASS/GOOD/良品/合格`）→ `OK`
   - 物料数为 0 → `EMPTY`
   - 其余（混合、无法识别的状态值）→ `MIXED`
4. `Message` 摘要：`MES 按框体条码命中 BOX-XXX：共 N 颗，OK a / NG b`（`material` 命中时相应变化）。
5. `RawPayload` 保存 MES 响应的**完整原始 JSON**，界面"MES 返回数据"面板原样展示，MES 侧扩展字段不丢失。
6. **数量不一致处理**：`materials` 数量 < 产品配置通道数 → 多余通道显示"待查询"；返回的通道号超出产品配置范围（位置映射下即物料数 > ChannelCount）→ 运行记录提示，数据仍完整保留在 RawPayload。

## 6. 客户端配置

配置位于程序目录 `scanner-app.json` 的 `MesApi` 节（界面" MES 接口设置"对话框可改，保存即生效，下一次查询使用新配置）：

| 字段 | 类型 | 默认值 | 说明 |
|---|---|---|---|
| `UseSimulation` | bool | `false` | `true` = 使用演示客户端（哈希伪数据，不联网）；`false` = 真实 HTTP 调用 |
| `RequestUrl` | string | `http://127.0.0.1:8080/api/query` | 完整 MES 查询地址，必须 http/https 绝对地址（演示模式不校验） |
| `Method` | string | `GET` | `GET`（条码放 URL 参数）或 `POST`（条码放 JSON 体） |
| `TimeoutSeconds` | int | `15` | 单次请求超时，1–120 秒 |

设置对话框内的**「测试请求」**按钮：不保存配置，用当前对话框里填写的参数即时发起一次查询（条码可在对话框内指定，默认 `BOX-DEMO-0001`），结果摘要直接显示在对话框上，用于现场快速定位"配置错 / 网络不通 / 条码未登记"。

## 7. 错误处理与防重入

- **防重入**：一次 MES 查询未完成时，新扫码（含扫码源推送）被忽略并写入运行记录（`忽略重复扫码`）。
- **取消语义**：窗体关闭或换型会取消进行中的查询；`OperationCanceledException` 统一显示"请求超时/已取消"。客户端内部超时与界面超时同源（同一配置值）。
- **不重试**：查询失败不自动重发，由操作工重新扫码触发；避免 MES 侧重复计数。
- **运行记录**：每次扫码、请求、结果、异常均写入"运行记录"面板（最近 100 条），含时间戳。

## 8. 联调步骤（ScannerMesApp ↔ mes-sim）

1. 启动 MES 仿真：双击 `mes-sim\mes-sim.exe`（默认端口 8080，数据文件 `mes-data.json`；换端口用 `mes-sim.exe -port 9000 -open=false`）。
2. 确认接口通：`curl "http://127.0.0.1:8080/api/query?barcode=BOX-DEMO-0001"`（或用 mes-data.json 中实际存在的框体/物料条码）。
3. 启动 ScannerMesApp → 「MES 接口设置」：确认 URL/方式/超时，点「测试请求」验证配置。
4. 扫码（键盘模拟：焦点在条码框，输入条码回车；或「配置扫码源」启用 TCP/PLC 来源），确认盘面渲染与 mes-sim 网页端该盘 `materials` 状态一致。
5. 异常路径验证：停掉 mes-sim 后扫码 → 应显示"查询失败：无法连接 MES 服务"；扫一个不存在的条码 → "未在 MES 中登记"；mes-sim 查询日志页面应能看到每次请求记录（含来源 IP）。

## 9. 接入正式 MES 前的确认清单

- [ ] MES 侧接口路径、方法、报文格式是否与第 4 节一致；不一致时改 `HttpMesInspectionClient` 的请求构造与响应映射（映射集中在该文件）。
- [ ] 通道编号是否由 MES 显式给出（当前按 `materials[]` 位置推定）。
- [ ] 状态码枚举是否超出 `OK/NG/复测` 集合；`OverallStatus` 推导规则是否需要按 MES 语义调整。
- [ ] 是否需要鉴权（Header/Token），当前客户端未附加任何认证头。
- [ ] 查询接口是否要求防重复/幂等约束（当前不自动重试）。
- [ ] 未命中条码按"查询失败"展示是否符合 MES 业务定义（也可能要求提示"跳过该盘"等流程动作）。
- [ ] 网络环境：工位 PC 到 MES 的 IP/端口/代理/防火墙放行。

## 10. 本次代码变更清单

| 文件 | 变更 |
|---|---|
| `BytequestorPlus\Core\Mes\Entity\MesApiSettings.cs` | 新增：`UseSimulation/RequestUrl/Method/TimeoutSeconds` 配置实体（自 ScannerMesApp 上移到框架） |
| `BytequestorPlus\Infrastructure\Mes\HttpMesInspectionClient.cs` | 新增：实现 `IMesInspectionClient` 的 HTTP 客户端（GET/POST、超时、mes-sim 契约解析与映射） |
| `ScannerMesApp\ScannerAppCatalog.cs` | 移除本地 `MesApiSettings`，改用框架实体；校验规则随 `UseSimulation` 调整；默认值指向 mes-sim |
| `ScannerMesApp\MesClientFactory.cs` | 新增：按配置创建演示/HTTP 客户端的小工厂 |
| `ScannerMesApp\MesApiSettingsForm.cs` | 新增演示模式开关、测试条码输入与「测试请求」按钮；更新提示文案 |
| `ScannerMesApp\InspectionForm.cs` | MES 客户端随配置热切换；状态文案与超时提示改用实际配置；关闭时释放客户端 |
| `ScannerMesApp\Program.cs` | 工作站打开时按配置创建 MES 客户端 |
| `ScannerMesApp\scanner-app.json` | MesApi 默认值更新为 mes-sim 地址 |
| `ScannerMesApp\现场验证说明.md` | 首段"不调用 MES"的表述更新 |
