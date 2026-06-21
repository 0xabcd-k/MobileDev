# MobileDev 项目开发方案总策划书

> 基于 20260620_design.md 设计文档

---

## 一、项目总览

本项目构建一套移动端 Vibe 编程协作系统，由三个独立程序组成：

| 程序 | 语言 | 部署位置 | 角色 |
|------|------|----------|------|
| **server** | Go | 公网服务器 | 中继服务器，转发 client 与 agent 之间的消息 |
| **agent** | Go | 开发者本地机器 | 代理，管理并执行功能插件 |
| **client** | Flutter | 移动设备 | 用户界面，操控远端 agent |

通信链路：`client <--wss--> server <--wss--> agent`

认证方式：共享密码（预共享密钥），client 和 agent 使用相同密码连接 server。

---

## 二、各程序详细设计

### 2.1 Server 中继服务器

#### 2.1.1 启动流程

```
读取配置(密码、端口) → 生成/加载自签名TLS证书 → 启动HTTPS服务
```

#### 2.1.2 模块划分

| 模块       | 职责                             |
|----------|--------------------------------|
| `config` | 解析命令行参数或配置文件（密码、监听端口、证书路径）     |
| `cert`   | 自签名证书生成与加载                     |
| `auth`   | 密码校验中间件，WebSocket 握手时验证        |
| `hub`    | 连接管理中心，维护 agent/client 在线列表与路由 |
| `ws`     | WebSocket 连接处理，消息解析与转发         |
| `api`    | REST 接口：查询 agent 列表            |

#### 2.1.3 核心数据结构

```
Connection {
    ID       string    // 唯一标识，此标识为agent、client安装时生成的uuid
    Type     string    // "agent" | "client"
    Name     string    // agent 名称（agent 连接时上报，此名称在agent配置中）
    Conn     *websocket.Conn
}

Hub {
    agents   map[string]*Connection
    clients  map[string]*Connection
    mu       sync.RWMutex
}

Message {
    From     string    // 发送方 ID
    To       string    // 接收方 ID
    Type     string    // agent2client、client2agent
    Action   string    // 动作类型
    SID      string    // 会话 ID，用于区分同一插件的多个并发会话
    Payload  any       // 数据负载
}
```

#### 2.1.4 WebSocket 消息协议

所有消息统一为字节流传输，用protobuf解析：

```protobuf
syntax = "proto3";

package message;

message Message {
  string from = 1;   // 发送方 ID
  string to = 2;     // 接收方 ID
  string type = 3;   // agent2client / client2agent
  string action = 4; // 动作类型
  string sid = 5;    // 会话 ID，区分同一插件的多个并发会话
  bytes payload = 6; // 数据负载
}
```

**Action 定义：**

| Action | 方向                                                 | 说明 |
|--------|----------------------------------------------------|------|
| `plugin.list` | client → server → agent                            | 获取已安装插件列表 |
| `plugin.list.resp` | agent → server → client                            | 返回插件列表 |
| `plugin.install` | client → server → agent                            | 安装插件（附下载链接） |
| `plugin.install.resp` | agent → server → client                            | 安装结果 |
| `plugin.delete` | client → server → agent                            | 删除插件 |
| `plugin.delete.resp` | agent → server → client                            | 删除结果 |
| `plugin.domain` | client → server → agent                            | 获取插件前端端点 |
| `plugin.domain.resp` | agent → server → client                            | 返回端点地址 |
| `plugin.do` | client → server → agent | 启动插件会话，附带初始指令（生成 sid） |
| `plugin.do.input` | client → server → agent | 会话中发送后续输入（回答问题、审批权限等） |
| `plugin.do.stream` | agent → server → client | 流式返回插件输出 |
| `plugin.do.cancel` | client → server → agent | 中断正在执行的会话 |
| `plugin.do.done` | agent → server → client | 会话结束 |

#### 2.1.5 REST API

| 端点 | 方法 | 说明 |
|------|------|------|
| `/api/agents` | GET | 查询在线 agent 列表（需密码 header） |
| `/ws/agent` | GET | agent WebSocket 连接入口 |
| `/ws/client` | GET | client WebSocket 连接入口 |

#### 2.1.6 认证流程

WebSocket 握手时通过 URL query 参数传递密码：
```
wss://server:port/ws/agent?token=sha256(password)&name=my-agent
wss://server:port/ws/client?token=sha256(password)
```
REST API 通过 Header 传递：
```
Authorization: Bearer sha256(password)
```

---

### 2.2 Agent 代理

#### 2.2.1 启动流程

```
读取配置(密码、server地址、插件目录) → 连接server WSS → 注册为agent → 监听消息 → 按命令执行
```

#### 2.2.2 模块划分

| 模块 | 职责 |
|------|------|
| `config` | 解析配置（server地址、密码、插件目录路径、agent名称） |
| `client` | WSS 客户端，负责与 server 通信、断线重连 |
| `plugin` | 插件管理：列举、安装、删除 |
| `executor` | 插件命令执行器：通过子进程调用插件可执行文件 |
| `handler` | 消息路由，根据 action 分发到对应处理逻辑 |

#### 2.2.3 插件目录结构

```
plugins/
├── plugin-a/
│   ├── main           # 可执行文件（或 main.py 等脚本）
│   └── manifest.json  # 插件元信息
├── plugin-b/
│   ├── main
│   └── manifest.json
```

**manifest.json 格式：**

```json
{
    "name": "plugin-a",
    "version": "1.0.0",
    "description": "示例插件",
    "entry": "main"
}
```

#### 2.2.4 插件交互协议

插件是独立可执行程序，agent 通过命令行参数调用：

| 命令 | 调用方式 | 说明 |
|------|----------|------|
| `--health` | `./main --health` | 返回 `{"status":"ok"}` 表示健康 |
| `--domain` | `./main --domain` | 返回前端端点 URL |
| `--do` | `./main --do` | 进入交互模式，agent 持续转发 stdin/stdout |

`--do` 模式下（每次调用对应一个 sid 会话）：
- 收到 `plugin.do` 时启动子进程，将初始 payload 写入 stdin
- 收到 `plugin.do.input` 时将后续输入写入同一子进程的 stdin（通过 sid 匹配）
- 读取子进程 stdout，逐块通过 `plugin.do.stream` 流式转发回 client
- 收到 `plugin.do.cancel` 时 kill 该 sid 对应的子进程
- 子进程退出时发送 `plugin.do.done`
- agent 按 sid 维护独立的子进程映射，支持同一插件多个并发会话

#### 2.2.5 插件安装流程

```
收到 plugin.install(url) → 下载压缩包 → 解压到 plugins/ 目录 → 校验 manifest.json → 赋予可执行权限 → 返回成功
```

---

### 2.3 Client 客户端 (Flutter)

#### 2.3.1 页面结构

```
入口配置页 → Agent列表页 → Agent详情页(插件列表) → 插件交互页
```

#### 2.3.2 页面详细设计

**页面1：入口配置页 (ConfigPage)**
- 输入项：Server 地址、密码
- 操作：保存配置、连接测试、进入主页
- 数据持久化：SharedPreferences

**页面2：Agent 列表页 (AgentListPage)**
- 显示所有在线 agent（名称、ID）
- 支持下拉刷新
- 点击进入 agent 详情

**页面3：Agent 详情页 (AgentDetailPage)**
- 显示已安装插件列表（名称、版本、描述）
- 提供「导入插件」按钮（输入下载链接）
- 提供删除插件操作（左滑或长按）
- 点击插件进入交互页

**页面4：插件交互页 (PluginPage)**
- 获取插件前端端点后通过 WebView 加载
- WebView 与 Flutter 之间通过 JS Bridge 通信
- JS Bridge 提供 send/receive 方法，底层走 WSS

#### 2.3.3 模块划分

| 模块 | 职责 |
|------|------|
| `config` | 配置管理与持久化 |
| `api_client` | HTTPS REST 客户端，用于查询 agent 列表等 |
| `ws_client` | WebSocket 连接管理，用于插件交互等实时通信 |
| `models` | 数据模型（Agent、Plugin 等） |
| `pages` | 各页面 UI |
| `services` | 业务逻辑层（plugin 操作） |
| `bridge` | WebView JS Bridge 封装 |

---

## 三、分步实施方案

### 第 1 步：Server 基础骨架

**目标：** 一个能启动的 HTTPS 服务，支持自签名证书和密码认证。

**实施内容：**
1. 初始化 Go module：`go mod init mobiledev/server`
2. 实现 `config` 模块：解析命令行参数 `--port`、`--password`
3. 实现 `cert` 模块：自动生成自签名 TLS 证书（RSA 2048，有效期 1 年），保存到磁盘供复用
4. 实现 `auth` 中间件：校验 `Authorization: Bearer sha256(password)` header
5. 实现健康检查端点 `GET /health`
6. 启动 HTTPS 服务

**自测用例：**
| # | 用例 | 预期结果 |
|---|------|----------|
| 1 | 启动 server，不带参数 | 报错提示必须指定 password |
| 2 | 启动 server `--password test123 --port 8443` | 正常启动，日志输出监听地址 |
| 3 | `curl -k https://localhost:8443/health` 不带 auth | 返回 401 |
| 4 | `curl -k -H "Authorization: Bearer $(echo -n test123 \| sha256sum \| cut -d' ' -f1)" https://localhost:8443/health` | 返回 200 `{"status":"ok"}` |
| 5 | 重启 server，证书文件已存在 | 复用已有证书，不重新生成 |

**手动冒烟：**
- 在一台机器上启动 server，用浏览器访问 `https://ip:8443/health`，确认浏览器提示证书不受信任但可继续访问，页面显示正确响应。

---

### 第 2 步：Server WebSocket 连接管理

**目标：** 支持 agent 和 client 通过 WSS 连接 server，server 维护在线列表。

**实施内容：**
1. 引入 `gorilla/websocket` 库
2. 实现 `hub` 模块：注册/注销连接，线程安全的在线列表
3. 实现 `/ws/agent` 端点：握手时校验 token，生成 agent ID，注册到 hub
4. 实现 `/ws/client` 端点：握手时校验 token，生成 client ID，注册到 hub
5. 实现 `GET /api/agents` 接口：返回当前在线 agent 列表
6. 实现心跳检测：ping/pong，超时断开

**自测用例：**
| # | 用例 | 预期结果 |
|---|------|----------|
| 1 | 用 wscat 连接 `/ws/agent?token=wrong` | 连接被拒绝（401） |
| 2 | 用 wscat 连接 `/ws/agent?token=正确&name=test-agent` | 连接成功，server 日志显示 agent 注册 |
| 3 | agent 连接后，调用 `/api/agents` | 返回包含 test-agent 的列表 |
| 4 | 断开 agent 连接，再调用 `/api/agents` | 列表中不再包含 test-agent |
| 5 | 同时连接 2 个 agent，查询列表 | 返回 2 个 agent |

**手动冒烟：**
- 用 `wscat` 工具模拟 agent 和 client 分别连接，确认列表接口正确反映在线状态。

---

### 第 3 步：Server 消息转发

**目标：** server 能根据消息中的 `to` 字段将消息路由到指定的 agent 或 client。

**实施内容：**
1. 实现消息解析：读取 WebSocket 消息，反序列化为 `Message` 结构
2. 实现路由逻辑：根据 `to` 字段在 hub 中查找目标连接并转发
3. 未找到目标时返回错误消息给发送方

**自测用例：**
| # | 用例 | 预期结果 |
|---|------|----------|
| 1 | client 发送消息 to=agent-1 | agent-1 收到该消息 |
| 2 | agent 发送消息 to=client-1 | client-1 收到该消息 |
| 3 | client 发送消息 to=不存在的id | client 收到错误响应 |
| 4 | 两个 client 分别给同一 agent 发消息 | agent 分别收到两条消息，from 字段不同 |

**手动冒烟：**
- 开三个终端：一个 agent、两个 client，通过 wscat 手动发送 JSON 消息验证转发。

---

### 第 4 步：Agent 基础骨架与连接

**目标：** agent 程序能启动、连接 server、自动重连。

**实施内容：**
1. 初始化 Go module：`go mod init mobiledev/agent`
2. 实现 `config` 模块：解析 `--server`、`--password`、`--name`、`--plugin-dir` 参数
3. 实现 `client` 模块：WSS 连接 server，支持忽略自签名证书
4. 实现断线重连：指数退避策略（1s、2s、4s...最大 30s）
5. 实现 `handler` 模块：消息分发框架

**自测用例：**
| # | 用例 | 预期结果 |
|---|------|----------|
| 1 | 启动 agent，server 未运行 | 持续重试连接，日志输出重试信息 |
| 2 | 先启动 server，再启动 agent | agent 连接成功，server 显示 agent 在线 |
| 3 | 手动断开 server | agent 检测到断连，自动重连 |
| 4 | 重启 server | agent 自动重连成功 |
| 5 | 通过 client 发送 `plugin.list` 给 agent | agent 收到消息（此时返回空列表或待实现提示） |

**手动冒烟：**
- 启动 server → 启动 agent → 在另一终端用 curl 调用 `GET /api/agents` 确认 agent 出现 → 手动 kill server → 观察 agent 重连日志 → 重启 server → 确认 agent 自动恢复连接。

---

### 第 5 步：Agent 插件管理

**目标：** agent 支持列举、安装、删除插件。

**实施内容：**
1. 实现 `plugin` 模块
   - `ListPlugins()`：扫描插件目录，读取各 manifest.json 返回列表
   - `InstallPlugin(url)`：下载压缩包（支持 .tar.gz / .zip）、解压、校验 manifest、赋予执行权限
   - `DeletePlugin(name)`：删除插件目录
   - `GetPluginHealth(name)`：调用 `./main --health` 检查
   - `GetPluginDomain(name)`：调用 `./main --domain` 获取端点
2. 在 `handler` 中注册对应 action 处理函数

**自测用例：**
| # | 用例 | 预期结果 |
|---|------|----------|
| 1 | 插件目录为空，调用 ListPlugins | 返回空数组 |
| 2 | 手动放一个合规插件到目录，调用 ListPlugins | 返回包含该插件的数组 |
| 3 | 调用 InstallPlugin，提供一个 HTTP 可下载的 tar.gz 插件包 | 插件安装到目录，ListPlugins 可见 |
| 4 | 安装后调用 GetPluginHealth | 返回 ok |
| 5 | 调用 DeletePlugin | 插件目录被删除，ListPlugins 不再包含 |
| 6 | InstallPlugin 给一个无效 URL | 返回下载失败错误 |
| 7 | 安装缺少 manifest.json 的压缩包 | 返回校验失败错误 |

**手动冒烟：**
- 编写一个最简单的 shell 脚本插件（echo 健康状态），打包成 tar.gz 放到本地 HTTP server → 通过 client 消息触发安装 → 查询列表确认 → 删除确认。

---

### 第 6 步：Agent 插件执行

**目标：** agent 支持调用插件的 `--do` 模式，流式转发 stdin/stdout。

**实施内容：**
1. 实现 `executor` 模块（按 sid 管理会话）
   - 收到 `plugin.do` 时生成 sid，启动子进程 `./main --do`，将初始 payload 写入 stdin
   - 收到 `plugin.do.input` 时按 sid 查找对应子进程，将后续输入写入 stdin
   - 读取子进程 stdout，逐块通过 WSS 发送 `plugin.do.stream` 消息给 client（携带 sid）
   - 收到 `plugin.do.cancel` 时按 sid 查找并 kill 对应子进程
   - 子进程退出后发送 `plugin.do.done` 消息，清理 sid 映射
2. 实现 `sessions` 映射：`map[string]*Session`，按 sid 维护子进程句柄、stdin pipe、所属 client 等
3. 支持并发：同一插件可被多个 sid 并行使用，互不干扰

**自测用例：**
| # | 用例 | 预期结果 |
|---|------|----------|
| 1 | 编写测试插件：`--do` 时每秒输出一行，共 5 行 | client 收到 5 条 stream 消息（均携带相同 sid），最后收到 done |
| 2 | 编写测试插件：`--do` 时读取 stdin 并 echo | client 先发 `plugin.do` 启动，再发 `plugin.do.input` 追加输入，收到 echo 回显 |
| 3 | 执行过程中 client 发送 `plugin.do.cancel` | 对应 sid 的插件进程被 kill，client 收到 done |
| 4 | 同一 client 对同一插件发起两次 `plugin.do`（不同 sid） | 两个会话各自独立收到对应输出 |
| 5 | 两个 client 同时对同一 agent 同一插件发起会话 | 各自 sid 独立，输出不混淆 |
| 6 | 插件执行异常退出（exit code != 0） | client 收到错误信息和 done，sid 映射被清理 |

**手动冒烟：**
- 编写一个倒计时插件（`--do` 时从 10 倒数到 1），通过 wscat 作为 client 触发执行，观察流式输出。

---

### 第 7 步：Client Flutter 基础框架

**目标：** Flutter 项目初始化，实现配置页和 WebSocket 通信层。

**实施内容：**
1. 创建 Flutter 项目 `flutter create client`
2. 实现 `config` 模块：使用 SharedPreferences 存储 server 地址和密码
3. 实现 `ws_client` 模块：
   - WSS 连接（支持自签名证书）
   - 消息发送/接收
   - 连接状态管理（连接中、已连接、断开）
   - 自动重连
4. 实现 `api_client` 模块：
   - HTTPS REST 请求（支持自签名证书）
   - 封装 `GET /api/agents` 调用
5. 实现 ConfigPage：
   - Server 地址输入框
   - 密码输入框
   - 「连接」按钮 → 调用 `GET /api/agents` 验证连通性 → 成功后跳转到 Agent 列表页
   - 连接状态指示

**自测用例：**
| # | 用例 | 预期结果 |
|---|------|----------|
| 1 | 首次打开 app | 显示配置页，字段为空 |
| 2 | 输入正确 server 地址和密码，点击连接 | 连接成功，跳转到 agent 列表页 |
| 3 | 输入错误密码，点击连接 | 显示连接失败提示 |
| 4 | 输入不可达的地址，点击连接 | 显示连接超时提示 |
| 5 | 连接成功后关闭 app 再打开 | 配置自动填充上次的值 |

**手动冒烟：**
- 手机上安装 app → 输入 server 地址和密码 → 连接 → 确认跳转成功。

---

### 第 8 步：Client Agent 列表与插件列表

**目标：** 展示在线 agent 列表和每个 agent 的已安装插件。

**实施内容：**
1. 实现 AgentListPage：
   - 进入时调用 REST API `GET /api/agents` 获取在线 agent 列表
   - 展示 agent 列表（名称）
   - 下拉刷新（重新调用 REST API）
   - 无 agent 时显示空状态提示
2. 实现 AgentDetailPage：
   - 进入时发送 `plugin.list` 请求
   - 展示插件列表（名称、版本、描述）
   - 「导入插件」按钮 → 弹窗输入下载链接 → 发送 `plugin.install`
   - 左滑删除插件 → 发送 `plugin.delete`

**自测用例：**
| # | 用例 | 预期结果 |
|---|------|----------|
| 1 | 有 2 个 agent 在线 | 列表显示 2 项 |
| 2 | 无 agent 在线 | 显示空状态提示 |
| 3 | 下拉刷新 | 列表重新加载 |
| 4 | 点击 agent 进入详情 | 显示该 agent 的插件列表 |
| 5 | 导入插件，输入有效 URL | 显示安装中 → 安装成功 → 列表刷新 |
| 6 | 删除插件 | 列表中移除该项 |

**手动冒烟：**
- 启动 server 和 2 个 agent → app 上查看列表 → 进入一个 agent → 导入测试插件 → 确认插件出现 → 删除 → 确认消失。

---

### 第 9 步：Client 插件交互页 (WebView)

**目标：** client 能通过 WebView 加载插件前端，并通过 JS Bridge 与 agent 侧插件通信。

**实施内容：**
1. 实现 PluginPage：
   - 进入时发送 `plugin.domain` 获取前端端点 URL
   - 使用 `webview_flutter` 加载该 URL
   - 进入时发送 `plugin.do` 启动会话，记录返回的 sid
2. 实现 JS Bridge：
   - Flutter 端注入 JS 对象 `MobileDev`
   - `MobileDev.send(data)`：插件前端调用此方法，Flutter 通过 WSS 发送 `plugin.do.input`（携带 sid）给 agent 侧插件
   - `MobileDev.onReceive(callback)`：注册回调，收到 `plugin.do.stream`（匹配 sid）时触发
   - `MobileDev.cancel()`：发送 `plugin.do.cancel` 中断当前会话
3. 在 `ws_client` 中按 sid 将 `plugin.do.stream` / `plugin.do.done` 消息路由到对应 WebView 的回调
4. 返回上一页时发送 `plugin.do.cancel` 终止会话

**自测用例：**
| # | 用例 | 预期结果 |
|---|------|----------|
| 1 | 点击插件，获取 domain 成功 | WebView 加载对应页面，会话（sid）已建立 |
| 2 | 插件前端调用 `MobileDev.send("hello")` | agent 收到 `plugin.do.input`（sid 匹配），插件 stdin 收到 "hello" |
| 3 | agent 侧返回数据 | 插件前端 onReceive 回调触发，sid 匹配 |
| 4 | 同时打开两个插件（两个 sid） | 各自输出独立，不混淆 |
| 5 | 网络断开再恢复 | WebView 不崩溃，重连后通信恢复 |
| 6 | 返回上一页 | 发送 cancel，WebView 关闭，agent 侧对应子进程终止 |

**手动冒烟：**
- 编写一个带简单 HTML 前端的测试插件（输入框 + 发送按钮 + 显示区域），`--domain` 返回本地 HTTP 地址 → app 上点击插件 → 在 WebView 中输入文字发送 → 确认收到回显。

---

### 第 10 步：示例插件开发

**目标：** 开发 1-2 个示例插件，验证整个系统端到端可用。

**实施内容：**

**示例插件1：Terminal（远程终端）**
- `--health`：返回 ok
- `--domain`：返回内嵌 xterm.js 的前端页面地址
- `--do`：启动一个 shell 进程，stdin/stdout 双向转发
- 前端：xterm.js 终端模拟器

**示例插件2：FileManager（文件浏览器）**
- `--health`：返回 ok
- `--domain`：返回文件浏览器前端页面地址
- `--do`：接收文件操作命令（list/read/write），操作本地文件系统
- 前端：简单的文件列表和编辑器界面

**自测用例：**
| # | 用例 | 预期结果 |
|---|------|----------|
| 1 | 安装 Terminal 插件 | 安装成功，health 返回 ok |
| 2 | 打开 Terminal 插件 | 显示终端界面 |
| 3 | 在终端输入 `ls` | 显示文件列表输出 |
| 4 | 在终端输入 `pwd` | 显示当前目录 |
| 5 | 安装 FileManager 插件 | 安装成功 |
| 6 | 打开 FileManager，浏览目录 | 显示文件列表 |

**手动冒烟：**
- 完整流程：手机 app → 连接 server → 选择 agent → 安装 Terminal 插件 → 打开 → 执行命令 → 确认输出正确。

---

### 第 11 步：健壮性与体验优化

**目标：** 提升系统稳定性和用户体验。

**实施内容：**
1. **Server 侧：**
   - 连接限速（防恶意连接）
   - 日志分级（info/warn/error）
   - 优雅关闭（graceful shutdown）
2. **Agent 侧：**
   - 插件执行超时控制
   - 插件崩溃隔离（子进程异常不影响 agent 主进程）
   - 下载进度上报
3. **Client 侧：**
   - 加载状态 UI（loading、error、empty state）
   - Toast 提示（操作成功/失败）
   - 网络状态感知与提示
   - 深色模式适配

**自测用例：**
| # | 用例 | 预期结果 |
|---|------|----------|
| 1 | 插件执行超过超时时间 | 自动终止，client 收到超时提示 |
| 2 | 插件进程 crash | agent 不受影响，client 收到错误提示 |
| 3 | 网络断开时操作 | 显示网络不可用提示，不 crash |
| 4 | 快速连续点击按钮 | 不产生重复请求 |

---

## 四、项目目录总体结构

```
MobileDev/
├── doc/                          # 文档
│   ├── 20260620_design.md        # 设计文档
│   └── 20260621_development_plan.md  # 本开发方案
├── server/                       # Go - 中继服务器
│   ├── go.mod
│   ├── main.go
│   ├── config/
│   │   └── config.go
│   ├── cert/
│   │   └── cert.go
│   ├── auth/
│   │   └── auth.go
│   ├── hub/
│   │   └── hub.go
│   ├── ws/
│   │   └── handler.go
│   ├── api/
│   │   └── handler.go
│   └── protocol/
│       └── message.go
├── agent/                        # Go - 代理
│   ├── go.mod
│   ├── main.go
│   ├── config/
│   │   └── config.go
│   ├── client/
│   │   └── ws_client.go
│   ├── plugin/
│   │   └── manager.go
│   ├── executor/
│   │   └── executor.go
│   └── handler/
│       └── handler.go
├── client/                       # Flutter - 客户端
│   ├── lib/
│   │   ├── main.dart
│   │   ├── config/
│   │   │   └── app_config.dart
│   │   ├── models/
│   │   │   ├── agent.dart
│   │   │   └── plugin.dart
│   │   ├── services/
│   │   │   ├── ws_client.dart
│   │   │   └── api_service.dart
│   │   ├── pages/
│   │   │   ├── config_page.dart
│   │   │   ├── agent_list_page.dart
│   │   │   ├── agent_detail_page.dart
│   │   │   └── plugin_page.dart
│   │   └── bridge/
│   │       └── js_bridge.dart
│   └── pubspec.yaml
└── plugins/                      # 示例插件
    └── terminal/
        ├── main.go
        ├── manifest.json
        └── frontend/
            └── index.html
```

---

## 五、技术依赖清单

### Server & Agent (Go)

| 依赖 | 用途 |
|------|------|
| `github.com/gorilla/websocket` | WebSocket 实现 |
| 标准库 `crypto/tls`, `crypto/x509` | 自签名证书 |
| 标准库 `os/exec` | 子进程管理 |
| 标准库 `archive/tar`, `compress/gzip`, `archive/zip` | 插件解压 |

### Client (Flutter)

| 依赖 | 用途 |
|------|------|
| `http` / `dio` | HTTPS REST 请求（查询 agent 列表等） |
| `web_socket_channel` | WebSocket 通信 |
| `webview_flutter` | 插件前端加载 |
| `shared_preferences` | 配置持久化 |
| `provider` | 状态管理 |

---

## 六、风险与注意事项

1. **自签名证书信任问题**：移动端 WebSocket 和 WebView 都需要处理自签名证书信任，Flutter 需要自定义 HttpOverrides。
2. **WebView 与 WSS 的协同**：WebView 加载的页面需要与 Flutter 主进程共享 WSS 连接，通过 JS Bridge 而非独立网络连接。
3. **插件安全性**：插件是任意可执行程序，需要在文档中明确告知用户风险。当前阶段不做沙箱隔离，依赖用户自行管理可信插件源。
4. **消息乱序**：WSS 本身保序，但多个 client 并发操作同一 agent 时，需要通过消息中的请求 ID 关联请求和响应。
5. **大文件传输**：当前设计基于 WebSocket 文本消息，不适合大文件传输。如有需求后续可扩展 Binary Frame 或独立 HTTP 通道。
