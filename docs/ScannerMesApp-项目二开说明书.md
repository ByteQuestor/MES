# ScannerMesApp 项目二开说明书

> 目标读者：要在此项目上做二次开发的工程师
> 覆盖三个核心技术点：**① 扫码枪读取条码 ② 向 MES 接口查询 ③ 接口返回数据处理**
> 所有类名、方法名、文件路径均来自当前源码，可直接按图索骥
> 配套文档：[ScannerMesApp-MES对接文档.md](./ScannerMesApp-MES对接文档.md)（通信契约与联调步骤）

---

## 0. 项目组成与代码地图

三个仓库/目录协作：

| 目录 | 角色 | 关键内容 |
|---|---|---|
| `E:\User\wzy\AUTOMATION\BytequestorPlus\BytequestorPlus\` | **框架类库**（net8.0） | 扫码抽象、MES 抽象、S7/TCP 驱动 |
| `E:\User\wzy\AUTOMATION\BytequestorPlus\ScannerMesApp\` | **工作站应用**（net8.0-windows, WinForms） | 界面、配置、程序入口 |
| `E:\User\wzy\AUTOMATION\MES\mes-sim\` | **MES 仿真服务**（Go 单文件 exe） | 提供 `/api/query` 等 HTTP 接口，用于联调 |

### 框架层（BytequestorPlus）关键文件

```
Core/                                       # 纯抽象与实体，不依赖任何 IO
├── Device/Abstraction/IBarcodeScannerClient.cs   # 路线①扫码器抽象（演示用）
├── Device/Abstraction/IBarcodeSource.cs          # 路线②扫码源抽象（现场用，核心）
├── Device/Entity/Scanner/
│   ├── ScanResult.cs            # 扫码终态 + 4 个 EventArgs（全项目通用）
│   ├── ScanSourceConfig.cs      # TCP/PLC 两路全部参数（配进 scanner-app.json）
│   ├── ScanSourceType.cs        # 枚举：PlcMapped / DirectTcp
│   └── ScannerProfile.cs        # 扫码器型号档案
├── Mes/Abstraction/IMesInspectionClient.cs       # MES 查询抽象（唯一入口）
├── Mes/Entity/MesTrayResult.cs   # MES 结果实体（盘级+通道级+原始JSON）
├── Mes/Entity/MesApiSettings.cs  # MES 接口配置（URL/Method/超时/演示开关）
├── Mes/Entity/ProductProfile.cs  # 产品（通道数/列数，决定盘面布局）
└── Common/Results/OperationResult.cs  # 统一成功/失败结果，避免异常流

Infrastructure/                              # 具体实现
├── Scanner/SimulatedBarcodeScannerClient.cs     # 演示扫码器（键盘输入→事件）
├── Scanner/Sources/BarcodeSourceBase.cs         # 扫码源骨架基类（二开必继承）
├── Scanner/Sources/BarcodeSourceFactory.cs      # 工厂 + BarcodeSourceHost
├── Scanner/Sources/DirectTcpScannerSource.cs    # TCP 直连读码器（X30）
├── Scanner/Sources/PlcMappedScannerSource.cs    # S7-1200 DB 轮询读码
├── Scanner/Sources/S7IsoTcpClient.cs            # 自实现 S7Comm 协议客户端
└── Mes/SimulatedMesInspectionClient.cs          # 演示 MES（哈希伪数据）
└── Mes/HttpMesInspectionClient.cs               # 真实 HTTP MES 客户端（对接核心）
```

### 应用层（ScannerMesApp）关键文件

```
Program.cs               # 入口；ScannerAppContext：选型菜单 → 打开工位
ScannerAppCatalog.cs     # scanner-app.json 的加载/校验/保存（所有配置的源头）
InspectionForm.cs        # 工位主界面：三技术点的汇合处（最重要的一坨）
MesApiSettingsForm.cs    # MES 接口设置对话框（含"测试请求"自检）
ScanSourceSettingsForm.cs# 扫码源参数对话框（TCP/PLC 点位）
MesClientFactory.cs      # 按配置创建演示/HTTP MES 客户端
ScannerSelectionForm.cs  # 启动后的扫码器选型菜单
ScannerAcquisitionForm.cs# 独立采集验证窗体（不在主流程中被引用，现场调试用）
scanner-app.json         # 运行配置：ScannerProfiles / Products / MesApi / ScanSource
```

### 一次完整扫码的数据流（三技术点总览）

```
读码器(X30) ──TCP──┐                        ┌─ SimulatedMesInspectionClient（伪数据）
                   ▼                        │
  DirectTcpScannerSource / PlcMappedScannerSource
       │ PublishResult(ScanResult)          │
       ▼                                    │
  InspectionForm.ScanSource_OnScanCompleted │  ②IMesInspectionClient.QueryTrayAsync
       │ BeginInvoke 到 UI 线程             │──▶ HttpMesInspectionClient（真实 HTTP）
       ▼                                    │        │ GET/POST {url}?barcode=xxx
  QueryMesAsync(barcode) ───────────────────┘        ▼
       ▲                                    ③ MesTrayResult{OverallStatus,Channels[],RawPayload}
       │                                    ┌────────┘
       ▼                                    ▼
  RenderChannels() 盘面网格渲染   _mesPayload 原始 JSON 展示
```

构建：`dotnet build BytequestorPlus\ScannerMesApp\ScannerMesApp.csproj`（应用引用框架项目，一处编译全链路）。

---

## 1. 核心技术点①：扫码枪读取条码

### 1.1 两条并行路线（必须先分清）

| 路线 | 抽象接口 | 实现类 | 用途 | 触发方式 |
|---|---|---|---|---|
| 路线①演示扫码 | `IBarcodeScannerClient` | `SimulatedBarcodeScannerClient` | 键盘敲条码回车，模拟一把扫码枪 | 焦点在条码框 + Enter，或"查询 MES"按钮 |
| 路线②扫码源 | `IBarcodeSource` | `DirectTcpScannerSource`、`PlcMappedScannerSource` | 真实硬件采集 | 界面"启动扫码源"按钮 |

两条路线在 `InspectionForm` 中**同时可用、互不干扰**：路线①经 `_scanner.BarcodeScanned` 事件，路线②经 `_activeSource.OnScanCompleted` 事件，最终都汇入同一个方法 `QueryMesAsync(barcode, scanTime)`。这是本项目最重要的架构决定——**MES 侧完全不关心条码从哪来**。

### 1.2 路线①：IBarcodeScannerClient（演示链路）

`Core/Device/Abstraction/IBarcodeScannerClient.cs`：

```csharp
public interface IBarcodeScannerClient : IAsyncDisposable
{
    ScannerProfile Profile { get; }
    bool IsConnected { get; }
    event EventHandler<BarcodeScannedEventArgs>? BarcodeScanned;
    Task<OperationResult> ConnectAsync(CancellationToken ct = default);
    Task<OperationResult> DisconnectAsync(CancellationToken ct = default);
}
```

唯一实现 `SimulatedBarcodeScannerClient`（19 行）：`ConnectAsync` 只把 `IsConnected=true`；界面在条码框敲 Enter → `SubmitBarcode(text)` → 触发 `BarcodeScanned` 事件，**与真实设备走完全相同的事件出口**。将来接真实厂商 SDK，就是再写一个 `XxxScannerClient : IBarcodeScannerClient` + 一个 Factory（`SimulatedBarcodeScannerClientFactory` 是范本），并在 `Program.cs` 的 `ScannerAppContext._scannerFactory` 换掉。

### 1.3 路线②：IBarcodeSource（现场采集链路，二开重点）

`Core/Device/Abstraction/IBarcodeSource.cs` 定义了四个事件 + 四个生命周期方法：

```csharp
public interface IBarcodeSource : IDisposable, IAsyncDisposable
{
    event EventHandler<ScanResultEventArgs>?      OnScanCompleted;   // ★拿到条码（业务唯一必需）
    event EventHandler<ScanConnectionChangedEventArgs>? OnConnectionChanged; // 连接/断线（UI 状态灯）
    event EventHandler<ScanTimeoutEventArgs>?     OnScanTimeout;     // 超时重试
    event EventHandler<ScanLogEventArgs>?         OnLog;             // 结构化日志（运行记录面板）

    void Init(ScanSourceConfig config);           // 注入配置（工厂 Create 后、Start 前调用）
    Task<OperationResult> StartAsync(CancellationToken ct = default);  // 启动后台采集
    Task<OperationResult> TriggerScanAsync(CancellationToken ct = default); // 软触发（当前两驱动均为被动）
    Task<OperationResult> ResetAsync(CancellationToken ct = default);  // 复位（当前只清本地状态）
}
```

**统一结果载体 `ScanResult`**（`Core/Device/Entity/Scanner/ScanResult.cs`）——所有来源的扫码终态：

| 属性 | 含义 | 备注 |
|---|---|---|
| `Barcode` | 条码字符串 | |
| `IsOk` / `IsError` | 设备级判定 | TCP 路线恒 `IsOk=true`（通道无硬件状态）；PLC 路线来自 OK/NG 位 |
| `IsComplete` | 本轮采集是否终结 | |
| `Source` | 来源标记 `PlcMapped`/`DirectTcp` | 自动由基类填充，用于日志区分 |
| `ScanTime` | 时间戳 | |
| `RetryCount` / `IsTimeout` | 重试/超时信息 | |

**二开规约：新来源只要产出 `ScanResult` 并经 `PublishResult()` 发布，MES 查询与界面渲染零改动。**

### 1.4 DirectTcpScannerSource 精读（TCP 直连 X30）

文件：`Infrastructure/Scanner/Sources/DirectTcpScannerSource.cs`。被动模式：PC 做 TCP **客户端**连读码器的 Server 口，不发任何触发指令（触发由 PLC 硬件 DI 完成）。

工作机制（对应源码行）：

1. **连接循环** `ReceiveLoopAsync`：`TcpClient` 连 `Config.DirectTcp.IpAddress:Port`，5 秒连接超时；成功后 `PublishConnection(true)`。
2. **CRLF 分帧** `ExtractFrames`：TCP 是字节流，存在粘包/拆包。代码把读到的字节累积进 `pending` 列表，循环找 `\r\n`（0x0D 0x0A）切出一帧；ASCII 解码后 `Trim('\0',' ','\t')`；**残余字节留在列表里等下一次 Read**。累计超 16KB 无 CRLF → 清缓存重连（防脏数据卡死）。
3. **防抖去重** `IsDuplicate`：同码在 `DuplicateDebounceMilliseconds`（默认 1200ms）窗口内只上报一次；旧记录按 4 倍窗口或 10 秒过期清理。快速重复扫同一盘不会重复打 MES。
4. **断线重连**：任何异常（含读码器主动断开 `read==0`）→ `PublishConnection(false)` → 等 `ReconnectIntervalMilliseconds`（默认 1500ms，钳制 250–30000）→ 重连。
5. 产出 `ScanResult{ IsOk=true, IsComplete=true, Barcode=..., Source=DirectTcp }`。

### 1.5 PlcMappedScannerSource 精读（S7-1200 轮询）

文件：`Infrastructure/Scanner/Sources/PlcMappedScannerSource.cs`。自实现 S7Comm（`S7IsoTcpClient`，ISO-on-TCP 102 端口，带 TSAP 0x0102/0x0100），**只读 DB、绝不写 PLC**。

默认点位（全部可在 `ScanSourceConfig.Plc` 配置）：

| 点位 | 默认 | 含义 |
|---|---|---|
| `Scan1ByteOffset/ScanCompleteBit` | 10 / 0 | `DB1.DBX10.0` 扫码完成位 |
| `Scan2ByteOffset + ScanOkBit/ScanNgBit` | 10 / 1、2 | `DBX10.1` OK、`DBX10.2` NG |
| `Scan3ByteOffset + BarcodeByteLength` | 12 / 128 | `DBB12` 起 128 字节 ASCII 条码（尾部 `\0` 截断） |
| `PollIntervalMilliseconds` | 100 | 轮询周期（50–5000） |

核心逻辑在 `PollLoopAsync`：

- `GetReadRange` 把三个区段合并成**一次连续 DB 读**（限制 ≤200 字节）；
- 完成位**上升沿触发**（`!previousComplete && complete`）才报扫码——首次采样只建基线，程序启动时 PLC 里遗留的高电平不会误报（源码 86–95 行，这是个易踩的坑，二开时别删）；
- `HandleRisingEdge` 里组 `ScanResult{ IsOk = ok&&!ng, IsError = ng||!ok }` ——**注意 PLC 路线的 OK/NG 是设备级判定**，与 MES 返回的物料状态是两码事，UI 各有展示位。

### 1.6 扫码源在界面上的接法（InspectionForm）

`InspectionForm.StartConfiguredSourceAsync()`（约 323 行起）是标准生命周期模板：

```
StopConfiguredSourceAsync()               # 先停旧的（退订 4 事件 + DisposeAsync）
_sourceFactory.Create(config.SourceType)  # BarcodeSourceFactory 按 SourceType new
source.Init(config)                       # 注入配置
订阅 OnScanCompleted/OnConnectionChanged/OnLog/OnScanTimeout
await source.StartAsync()                 # 失败则 Dispose 并提示
```

**线程模型（重要）**：四个事件都从**后台任务线程**回调。`ScanSource_OnScanCompleted` 首行就是 `if (InvokeRequired) BeginInvoke(...)` 回 UI 线程。你新增的任何扫码源同样会被后台线程调用，UI 侧一律照此处理，否则 WinForms 偶发跨线程异常。

### 1.7 二开配方 A：新增一种扫码来源（如串口扫码枪 / 厂商 SDK / HTTP 读码器）

四步，全部有现成范本：

**第 1 步**：加枚举 `Core/Device/Entity/Scanner/ScanSourceType.cs`

```csharp
public enum ScanSourceType { PlcMapped, DirectTcp, SerialPort /* 新增 */ }
```

**第 2 步**：继承骨架 `BarcodeSourceBase`（它已替你实现事件出口与 Init 校验，你只写通信）：

```csharp
public sealed class SerialPortScannerSource : BarcodeSourceBase
{
    protected override ScanSourceType SourceType => ScanSourceType.SerialPort;

    public override Task<OperationResult> StartAsync(CancellationToken ct = default)
    {
        // 打开串口、起后台读任务；成功 return OperationResult.Success();
    }

    private void OnBarcodeArrived(string barcode)
    {
        Log($"收到扫码结果：{barcode}");                 // → OnLog 事件 → 运行记录面板
        PublishResult(new ScanResult                     // → OnScanCompleted → 自动进入 MES 查询
        {
            IsComplete = true, IsOk = true, Barcode = barcode, ScanTime = DateTimeOffset.Now
        });  // Source 由基类自动填 SerialPort
    }

    public override Task<OperationResult> TriggerScanAsync(CancellationToken ct = default) { ... }
    public override Task<OperationResult> ResetAsync(CancellationToken ct = default) { ... }
    public override ValueTask DisposeAsync() { /* 关串口、等任务退出 */ }
}
```

**第 3 步**：注册工厂 `Infrastructure/Scanner/Sources/BarcodeSourceFactory.cs` 的 switch 加一行：

```csharp
ScanSourceType.SerialPort => new SerialPortScannerSource(),
```

**第 4 步**：参数加进 `ScanSourceConfig.cs`（新建 `SerialScanConfig` 类挂到 `ScanSourceConfig`，参照 `DirectTcpScanConfig`），如需界面可编辑再改 `ScanSourceSettingsForm`。

到此为止：选中新来源 → 启动 → 扫码 → MES 查询 → 盘面渲染，**整条链路自动打通，MES 与渲染代码零改动**。这就是抽象层的价值。

---

## 2. 核心技术点②：向 MES 接口查询

### 2.1 唯一抽象：IMesInspectionClient

`Core/Mes/Abstraction/IMesInspectionClient.cs`：

```csharp
public interface IMesInspectionClient
{
    Task<OperationResult<MesTrayResult>> QueryTrayAsync(
        string barcode, ProductProfile product, CancellationToken ct = default);
}
```

设计意图（源码注释原话）："HTTP 协议、鉴权与供应商 JSON 映射由具体客户端负责"。**界面只认这个接口**——换 MES 供应商 = 换一个实现类，`InspectionForm` 一行不改。

返回值用 `OperationResult<T>`（`Core/Common/Results/OperationResult.cs`）而不是抛异常：`IsSuccess` + `ErrorMessage` + `Value`。**网络失败是产线常态而非程序错误**，走返回值让 UI 分支统一。

### 2.2 两个实现 + 工厂选择

| 实现 | 行为 | 何时用 |
|---|---|---|
| `SimulatedMesInspectionClient` | `Task.Delay(650)` 后按条码 FNV 哈希生成每通道 OK/RETEST/NG | `MesApi.UseSimulation=true`（离线演示/培训） |
| `HttpMesInspectionClient` | 真实 HTTP 请求 | `UseSimulation=false`（默认） |

选择逻辑在应用层 `ScannerMesApp/MesClientFactory.cs`（3 行）。配置热切换：`InspectionForm.OpenMesSettings()` 保存成功后调 `SwapMesClient()`——销毁旧客户端、按新配置重建，下一次扫码即生效。

### 2.3 HttpMesInspectionClient 内部（对接核心，二开主要改这里）

文件：`Infrastructure/Mes/HttpMesInspectionClient.cs`。`QueryTrayAsync` 的执行顺序：

```
1. 入参校验：条码非空 / 产品通道数有效 / RequestUrl 是合法 http(s)
2. 构造请求  BuildRequest()：
   ├─ Method=POST → POST {url}，body={"barcode":"xxx"}，Content-Type=application/json
   └─ Method=GET  → GET {url}?barcode=UrlEncode(xxx)（保留 URL 上已有参数）
3. 发送：HttpClient.SendAsync，超时控制 = LinkedCancellationTokenSource
   + CancelAfter(TimeoutSeconds)（HttpClient.Timeout 设 InfiniteTimeSpan，
     统一走取消令牌，与窗体关闭取消同一条通道）
4. 错误分类（全部 return Failure，绝不抛）：
   ├─ HTTP 非 2xx        → "MES 返回 HTTP <码>：<响应体前200字>"
   ├─ 非 JSON            → "MES 响应不是合法 JSON：..."
   ├─ 信封 code != 0     → Failure(message)
   ├─ found=false        → "条码 X 未在 MES 中登记（未命中任何料盘）"
   ├─ found=true 无 tray → "MES 命中条码但未返回料盘数据"
   └─ 超时               → "MES 请求超过 N 秒未响应"（区分外部取消并上抛）
   └─ 网络异常           → "无法连接 MES 服务：<原因>"
5. 成功 → MapResult() 把 JSON 映射成 MesTrayResult（见第 3 章）
```

信封 DTO（私有类，在文件底部，与 mes-sim 契约一一对应）：

```
MesEnvelope      { Code, Message, Data }
MesQueryData     { Found, MatchType, matchedBarcode, Tray }
MesTrayPayload   { Id, BoxBarcode, Remark, Materials[] }
MesMaterialPayload { Barcode, Status }
```

反序列化用 `PropertyNameCaseInsensitive = true`——MES 字段大小写不同也能吃。

### 2.4 配置体系

`Core/Mes/Entity/MesApiSettings.cs`（4 个属性）→ 序列化在 `scanner-app.json` 的 `MesApi` 节：

```json
"MesApi": {
  "UseSimulation": false,          // true=演示伪数据不联网
  "RequestUrl": "http://127.0.0.1:8080/api/query",
  "Method": "GET",                 // GET 或 POST
  "TimeoutSeconds": 15
}
```

加载/校验/保存：`ScannerAppCatalog.Load()/Validate()/Save()`（原子写：先写 .tmp 再 Move 覆盖）。界面入口：主界面右上「MES 接口设置」→ `MesApiSettingsForm`，内含**「测试请求」按钮**——不保存配置、按对话框当前值即时发一次真实请求（默认条码 `BOX-DEMO-0001`），用耗时+结果区分"配置错 / 网络不通 / 条码未登记"。

### 2.5 二开配方 B：真实 MES 报文与 mes-sim 不一致时改哪里

只动 `HttpMesInspectionClient.cs` 一个文件，三个位置：

**① 请求侧** `BuildRequest()`：MES 要多带字段（如产品号、工位）时——POST 分支把 `new { barcode }` 换成 `new { barcode, productCode = product.ProductCode }`（注意需把 `product` 传进该方法）；GET 分支同理拼 query。

**② 信封结构** `MesEnvelope` 等 4 个 DTO：按真实 MES 的响应字段增删改（比如有的 MES 外层是 `{"success":true,"result":{...}}`，就改属性名/加 `[JsonPropertyName]`）。

**③ 判定入口** `QueryTrayAsync` 中部那段 `if (envelope.Code != 0)... if (!envelope.Data.Found)...`：按真实 MES 的"业务失败/未命中"语义调整。

> 经验：先用 `curl`（或「测试请求」按钮）打一条真实响应贴进来，照着改 DTO，比读 MES 文档快。

---

## 3. 核心技术点③：接口返回数据处理

### 3.1 数据模型链（三级转换，各司其职）

```
HTTP JSON 原文
  └─(DTO 反序列化)→ MesEnvelope/MesTrayPayload     # 仅 HttpMesInspectionClient 内部可见
  └─(MapResult 映射)→ MesTrayResult                 # ★跨层标准实体（Core/Mes/Entity）
  └─(InspectionForm 消费)→ 盘面网格 / 状态文字 / 原始JSON面板
```

`MesTrayResult`（`Core/Mes/Entity/MesTrayResult.cs`）：

```csharp
public sealed record MesTrayResult(
    string OverallStatus,                  // 盘级判定："OK"/"NG"/"MIXED"/"EMPTY"
    string? Message,                       // 人读摘要，直接显示在状态栏
    IReadOnlyList<MesChannelResult> Channels,  // 每通道判定
    JsonElement RawPayload);               // MES 原始响应整棵 JSON（不丢任何字段）

public sealed record MesChannelResult(
    int ChannelNumber,                     // 通道号（盘面格子定位）
    string StatusCode,                     // 原样状态字符串（"OK"/"NG"/MES 自定义）
    string? DisplayText = null);           // 格子里显示的文字（当前=物料条码）
```

### 3.2 MapResult 映射规则（改判定逻辑就改这一个方法）

`HttpMesInspectionClient.MapResult()`，四条规则：

1. **通道号 = 物料在 `materials[]` 数组中的位置 + 1**。第 1 颗电池 → 通道 1。MES 契约没给显式通道号，这是本项目的一个约定；MES 若给了（如 `channel` 字段），把 `index + 1` 换成 `material.Channel` 即可（配方 C-①）。
2. `StatusCode = status` 原样字符串保留（MES 加新状态码不需要改客户端）；`DisplayText = barcode`（盘面格显示物料条码）。
3. **盘级 OverallStatus 推导**：任一 NG → `"NG"`；全部 OK → `"OK"`；空盘 → `"EMPTY"`；其余混合 → `"MIXED"`。OK/NG 判定集合：`OK/PASS/GOOD/良品/合格` 与 `NG/FAIL/BAD/不良/不合格`（大小写不敏感）。
4. `RawPayload = document.RootElement.Clone()`——**整棵响应 JSON 原样保留**。MES 返回的任何扩展字段（工单、产线、操作工…）都不会丢，界面"MES 返回数据"面板原样展示。

### 3.3 界面消费全流程：InspectionForm.QueryMesAsync（约 587 行）

这是三个技术点的汇合处，建议整段精读。流程：

```
QueryMesAsync(barcode, scannedAt)
  ① 防重入：if (_busy) → 忽略并记日志"忽略重复扫码"（上一次查询没完成时新扫码直接丢）
  ② _busy=true；禁用查询按钮/产品下拉；状态栏"查询中…"
  ③ 新建 CancellationTokenSource(TimeoutSeconds)——注意超时兜底在两层：
     界面 CTS 与客户端内部 CancelAfter 同一个配置值
  ④ await _mes.QueryTrayAsync(barcode, product, token)
  ⑤ 失败分支（result.IsSuccess==false）：状态栏红色"查询失败"+ErrorMessage，
     原始JSON面板显示错误文本，盘面 RenderChannels(null) 全部灰格
  ⑥ 成功分支：
     - 通道号越界检查：ChannelNumber 超出产品配置 1..ChannelCount → 记日志
       "MES 通道数据超出当前产品配置"，数据仍保留在 RawPayload
     - _overallState.Text = OverallStatus；颜色 = StatusColor()
     - _mesPayload 显示 RawPayload 缩进 JSON
     - RenderChannels(Channels, product) 画盘面
  ⑦ catch OperationCanceledException → "请求超时"；catch Exception → "查询异常"
  ⑧ finally：_busy=false，恢复按钮，清空并聚焦条码框（准备好下一枪）
```

**渲染细节 `RenderChannels()`（约 660 行）**：按产品 `Columns` 计算格子宽度（70–118px 钳制）排成 N 行 × Columns 列；每格 = 通道号 + 状态文字，左侧色条着色。`map = results.GroupBy(ChannelNumber).Last()`——**MES 若对同一通道返回多条，取最后一条**。

**着色表 `StatusColor()`（约 718 行）**：

| 输入 | 颜色 |
|---|---|
| `OK/PASS/GOOD/良品/合格` | 绿 `42,163,111` |
| `NG/FAIL/BAD/不良/不合格` | 红 `215,75,75` |
| `RETEST/RECHECK/复测/待复测` | 黄 `231,166,44` |
| 其他任何值 / null（待查询） | 灰蓝 `126,145,166` / 浅灰 |

### 3.4 数量一致性与产品换型

- 物料数 < 产品通道数：多余格子灰格"待查询"（合理——盘没摆满）。
- 物料数 > 通道数：越界数据不画格但保留在 RawPayload，日志提示。
- **换产品 = 换 `ProductProfile`**：工具栏下拉 + 「应用换型」（`Changeover_Click`：取消在途查询、清界面、按新产品重画空盘）。新产品在 `scanner-app.json` 的 `Products[]` 加一条即可：`{Id, DisplayName, ProductCode, ChannelCount, Columns}`，行数自动 = `⌈ChannelCount/Columns⌉`。

### 3.5 二开配方 C：返回数据侧常见改造

| 需求 | 改哪里 | 怎么改 |
|---|---|---|
| C-① MES 提供显式通道号 | `HttpMesInspectionClient.MapResult()` | `MesMaterialPayload` 加 `Channel` 属性；`index+1` 改 `material.Channel` |
| C-② 新增状态颜色（如"待定"黄色） | `InspectionForm.StatusColor()` | 加一个 `if (value is "PENDING" or "待定") return Color...;` |
| C-③ 把 MES 扩展字段显示到界面（如工单号） | `InspectionForm.QueryMesAsync` 成功分支 | `response.RawPayload.GetProperty("workOrder").GetString()`，放到 `_trayMeta` 或新 Label |
| C-④ 盘级判定规则改为"MES 说什么就是什么" | `HttpMesInspectionClient.MapResult()` | 删推导，`OverallStatus = tray` 里 MES 给的盘级字段 |
| C-⑤ 查询成功后上报 PLC / 写库 | `InspectionForm.QueryMesAsync` 成功分支 | 在 `AddActivity` 之后插入你的上报调用（拿 `response.OverallStatus` 与 `Channels`） |
| C-⑥ 换新盘型 | `scanner-app.json` Products | 加配置项，无需改码 |

---

## 4. 二开任务速查表（想做什么 → 动哪里）

| 我想… | 文件 | 方法/位置 |
|---|---|---|
| 接一种新读码器（串口/SDK） | `ScanSourceType.cs` + 新建 `XxxSource.cs` + `BarcodeSourceFactory.cs` | 配方 A（§1.7） |
| 改 TCP 分帧符（非 CRLF） | `DirectTcpScannerSource.cs` | `ExtractFrames` 里找 `\r\n` 的循环 |
| 改 PLC 点位/轮询 | `ScanSourceConfig.cs`（或界面 `ScanSourceSettingsForm`） | `PlcMappedScanConfig` 各字段 |
| 对接真实 MES 报文 | `HttpMesInspectionClient.cs` | `BuildRequest` / DTO / `QueryTrayAsync` 判定段（配方 B） |
| MES 加鉴权头 | `HttpMesInspectionClient.cs` | `BuildRequest` 里 `request.Headers.Add(...)`；配置加到 `MesApiSettings` |
| 改盘级判定推导 | `HttpMesInspectionClient.cs` | `MapResult` |
| 改格子颜色/文案 | `InspectionForm.cs` | `StatusColor` / `CreateChannelTile` |
| 加产品盘型 | `scanner-app.json` | `Products[]` |
| 查询后联动 PLC/写库 | `InspectionForm.cs` | `QueryMesAsync` 成功分支（配方 C-⑤） |
| 改默认 MES 地址/演示开关 | `scanner-app.json` / `MesApiSettings.cs` | `MesApi` 节 |

## 5. 常见坑与调试技巧

1. **跨线程 UI**：扫码源 4 个事件全在后台线程；改 UI 必须像 `ScanSource_OnScanCompleted` 一样先 `InvokeRequired/BeginInvoke`。
2. **PLC 完成位基线**：`PlcMappedScannerSource` 首次采样只建边沿基线不报扫码——不是 bug；删掉会启动即误报一次。
3. **TCP 粘包**：不要用"一次 Read = 一条条码"的写法；`ExtractFrames` 的 pending 缓冲 + CRLF 切分必须保留。
4. **async void**：仅限 WinForms 事件处理器（`QueryMesAsync` 由 `_ = QueryMesAsync(...)` fire-and-forget + `_busy` 防重入兜底）；业务代码一律 `async Task`。
5. **JSON 大小写**：信封反序列化已开 `PropertyNameCaseInsensitive`；但 `RawPayload.GetProperty("xxx")` 是**大小写敏感**的，取扩展字段时注意。
6. **联调三步定位**：`curl` 打 `/api/health`（服务活着吗）→ 应用内「测试请求」（配置对吗/通吗）→ 扫码看运行记录面板（每步都有带时间戳日志，来源带 `Source=` 前缀）。
7. **mes-sim 数据**：`mes-data.json` 手改后要重启 exe；查询日志只留最近 500 条。
8. **配置文件在哪**：一切运行配置在 exe 同目录 `scanner-app.json`（`ScannerAppCatalog.ConfigPath`），首次运行自动生成演示默认值；`Save()` 是 .tmp+Move 原子写，不会写坏。
