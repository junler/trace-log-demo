# 分布式链路追踪演示

这是一个使用 OpenTelemetry 演示分布式链路追踪的项目，包含两个微服务：

- **service-a**: 运行在 `localhost:8080`，提供 `/users/:id` 接口
- **service-b**: 运行在 `localhost:8081`，提供 `/info` 接口

初始代码使用的是OpenTelemetry-go的Example
https://pkg.go.dev/go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin

## OpenTelemetry

- OpenTelemetry（简称 OTel）就是现代微服务里做 可观测性（Observability） 的标准方案。OTel 不是一个单独软件，它是一个生态：

- OpenTelemetry 是一套统一的规范 + SDK + 工具，用来采集和传递 Trace、Metrics、Logs，并把它们导出到 Jaeger、Prometheus、Grafana、Tempo、Elastic 等系统。

- Trace（链路追踪）
一个请求从 API Gateway → Service A → DB → Service B 全过程
关键概念：TraceId、SpanId

OpenTelemetry 不负责写文件日志，Zap 写文件 + OpenTelemetry 注入 TraceId。

### 日志库：zap性能高一些，logrus说是好看一些

- https://github.com/uber-go/zap 需要配置成json格式输出
- https://github.com/sirupsen/logrus
- https://github.com/rs/zerolog  默认结构化日志（JSON）

日志想要 JSON 格式 还是普通文本？

```
Gin 中间件每次请求自动生成 trace：

r.Use(func(c *gin.Context) {
	ctx := c.Request.Context()

	traceID, _ := TraceFields(ctx)

	c.Set("trace_id", traceID)
	c.Next()
})

import (
	"context"

	"go.opentelemetry.io/otel/trace"
)

func TraceFields(ctx context.Context) (string, string) {
	span := trace.SpanFromContext(ctx)
	sc := span.SpanContext()

	if !sc.IsValid() {
		return "", ""
	}

	return sc.TraceID().String(), sc.SpanID().String()
}
```

## 功能特性

1. **统一日志管理（Zap）**: 使用 uber-go/zap 作为结构化日志库
2. **环境感知日志输出**:
   - 开发环境（`ENV=dev` 或未设置）：彩色控制台输出
   - 生产/测试环境（`ENV=prod` 或 `ENV=test`）：JSON 格式写入日志文件
3. **访问日志包含 trace_id**: 每个请求的访问日志都包含 trace_id，方便关联分布式追踪
4. **微服务调用**: service-a 会调用 service-b，trace context 自动传播
5. **生产级采样**: 使用 1% 采样率（`ParentBased(TraceIDRatioBased(0.01))`），父子 span 保持一致
6. **完整的 span 链路**: 可以看到完整的请求调用链
7. **详细的业务日志**: 记录请求参数、返回结果等业务信息

## 运行方式

### 1. 启动 service-b（开发环境）

```bash
cd service-b
go mod tidy
go run main.go
```

### 2. 启动 service-a（开发环境）

```bash
cd service-a
go mod tidy
go run main.go
```

### 3. 生产环境运行

```bash
# 设置环境变量
export ENV=prod

# 启动服务
cd service-a && go run main.go
cd service-b && go run main.go
```

日志会自动写入 `logs/` 目录下对应的日志文件。

### 4. 发送测试请求

```bash
curl http://localhost:8080/users/123
```

## 观察输出

### 开发环境日志示例（控制台彩色输出）

```
2026-02-13 15:30:45	INFO	HTTP Request	{"trace_id": "fbf60e601c37cbaf03007ac6930c4278", "method": "GET", "path": "/users/123", "status": 200, "latency": "13.5ms", "client_ip": "::1"}
2026-02-13 15:30:45	INFO	Getting user with id=123	{"trace_id": "fbf60e601c37cbaf03007ac6930c4278", "service": "UserService"}
2026-02-13 15:30:45	INFO	Calling service-b: GET http://localhost:8081/info	{"trace_id": "fbf60e601c37cbaf03007ac6930c4278", "service": "UserService"}
```

### 生产环境日志示例（JSON 格式）

```json
{"level":"info","ts":"2026-02-13 15:30:45","msg":"HTTP Request","trace_id":"fbf60e601c37cbaf03007ac6930c4278","method":"GET","path":"/users/123","status":200,"latency":13500000,"client_ip":"::1"}
{"level":"info","ts":"2026-02-13 15:30:45","msg":"Getting user with id=123","trace_id":"fbf60e601c37cbaf03007ac6930c4278","service":"UserService"}
```

日志文件位置：
- `logs/service-a-2026-02-13.log` - service-a 常规日志
- `logs/service-a-error-2026-02-13.log` - service-a 错误日志
- `logs/service-b-2026-02-13.log` - service-b 常规日志
- `logs/service-b-error-2026-02-13.log` - service-b 错误日志

### Trace Span 输出

当请求被采样时（1% 概率），会输出完整的 trace span 信息：

- `GET /users/:id` - service-a 的入口 span
- `getUser` - service-a 的业务逻辑 span
- `HTTP GET` - service-a 调用 service-b 的客户端 span
- `GET /info` - service-b 的入口 span
- `getInfo` - service-b 的业务逻辑 span
- `gin.renderer.html` - 模板渲染 span

所有这些 span 共享同一个 `TraceID`，通过 `Parent` 字段形成调用链。

## 架构说明

```
请求 → service-a:8080 → service-b:8081
         |                    |
         ↓                    ↓
    访问日志(trace_id)    访问日志(trace_id)
         |                    |
         ↓                    ↓
      Trace Span          Trace Span
```

## TraceID 传播机制

### OpenTelemetry 如何生成和传播 TraceID

1. **TraceID 生成**
   - TraceID 由 OpenTelemetry SDK 在创建第一个 span 时自动生成
   - 在 service-a 中，当请求到达时，`otelgin.Middleware` 创建根 span 并生成 128 位的 TraceID
   - TraceID 格式：32 个十六进制字符（例如：`fbf60e601c37cbaf03007ac6930c4278`）

2. **跨服务传播**
   - Service-a 调用 service-b 时使用 `otelhttp.NewTransport`
   - 该 Transport 自动将 trace context 注入到 HTTP 请求头中
   - 使用 **W3C Trace Context** 标准：`traceparent` header
   - 格式：`traceparent: 00-{trace-id}-{span-id}-{flags}`
   - 示例：`traceparent: 00-fbf60e601c37cbaf03007ac6930c4278-0093a864b80ec051-01`

3. **接收端提取**
   - Service-b 的 `otelgin.Middleware` 从请求头提取 trace context
   - 使用相同的 TraceID 创建新的 child span
   - 这样整个调用链共享同一个 TraceID

### 关键代码位置

**Service-A（发送端）**：
```go
// 使用 otelhttp.NewTransport 包装标准 http.RoundTripper
// 它会在发送请求前自动注入 trace context 到 HTTP headers
client := &http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)}
req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
resp, err := client.Do(req)

// otelhttp.NewTransport 做的事情：
// 1. 从 context 中提取当前的 trace context
// 2. 将 TraceID 和 SpanID 注入到 HTTP 请求头
//    例如：traceparent: 00-fbf60e601c37cbaf03007ac6930c4278-0093a864b80ec051-01
// 3. 调用实际的 HTTP Transport 发送请求
// 4. 创建一个 client span 记录此次 HTTP 调用
```

**Service-B（接收端）**：
```go
// otelgin.Middleware 自动提取 trace context
r.Use(otelgin.Middleware("service-b"))
```

**Propagator 配置**：
```go
// 在 initTracer 中配置
otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
    propagation.TraceContext{},  // W3C Trace Context
    propagation.Baggage{},       // W3C Baggage
))
```

### HTTP RoundTripper 拦截机制

Go 的 `http.Client` 使用 `RoundTripper` 接口执行 HTTP 请求：

```go
type RoundTripper interface {
    RoundTrip(*Request) (*Response, error)
}
```

`otelhttp.NewTransport` 实现了这个接口，形成拦截器链：

```
请求 → otelhttp.Transport（注入 headers）→ http.DefaultTransport（实际网络请求）→ 响应
        ↓
    创建 client span
    记录请求信息
```

这种设计模式让 OpenTelemetry 可以**无侵入地**拦截所有 HTTP 请求，自动处理 trace 传播。

## 不使用 OpenTelemetry 的替代方案

### 1. 手动 TraceID 传播（最简单）

**优点**：轻量、无依赖、完全可控  
**缺点**：需要手动处理所有逻辑

```go
// 生成 TraceID
import "github.com/google/uuid"
traceID := uuid.New().String()

// 发送端：手动注入 header
req.Header.Set("X-Trace-Id", traceID)

// 接收端：从 header 提取
traceID := c.GetHeader("X-Trace-Id")
if traceID == "" {
    traceID = uuid.New().String()
}
```

### 2. Jaeger Client SDK

**优点**：成熟稳定、Jaeger 原生支持  
**缺点**：与 Jaeger 强绑定

```go
import (
    "github.com/uber/jaeger-client-go"
    "github.com/uber/jaeger-client-go/config"
)

cfg := config.Configuration{
    ServiceName: "service-a",
    Sampler: &config.SamplerConfig{
        Type:  jaeger.SamplerTypeConst,
        Param: 1,
    },
}
tracer, closer, _ := cfg.NewTracer()
defer closer.Close()
```

### 3. Zipkin Client

**优点**：轻量、与 Zipkin 原生集成  
**缺点**：功能相对简单

```go
import (
    "github.com/openzipkin/zipkin-go"
    "github.com/openzipkin/zipkin-go/reporter/http"
)

reporter := http.NewReporter("http://localhost:9411/api/v2/spans")
tracer, _ := zipkin.NewTracer(reporter)
```

### 4. 云厂商 SDK

**AWS X-Ray**：
```go
import "github.com/aws/aws-xray-sdk-go/xray"
xray.Configure(xray.Config{ServiceVersion: "1.0.0"})
```

**Google Cloud Trace**：
```go
import "cloud.google.com/go/trace"
client, _ := trace.NewClient(ctx, projectID)
```

**阿里云 ARMS**：
```go
import "github.com/aliyun/aliyun-log-go-sdk"
// 使用阿里云 SLS 日志服务
```

### 5. APM 工具集成

- **DataDog APM**：`github.com/DataDog/dd-trace-go`
- **New Relic**：`github.com/newrelic/go-agent`
- **Elastic APM**：`go.elastic.co/apm`
- **SkyWalking**：`github.com/apache/skywalking-go`

### 方案对比

| 方案 | 复杂度 | 功能完整性 | 厂商绑定 | 推荐场景 |
|------|--------|----------|---------|---------|
| 手动传播 | 低 | 基础 | 无 | 简单场景、学习用途 |
| OpenTelemetry | 中 | 完整 | 无（开放标准） | **生产推荐** |
| Jaeger Client | 中 | 完整 | Jaeger | 已使用 Jaeger |
| Zipkin Client | 低 | 中等 | Zipkin | 轻量级场景 |
| 云厂商 SDK | 高 | 完整 | 强 | 云原生应用 |
| APM 工具 | 低 | 完整 | 中 | 商业项目 |

### 推荐选择

1. **新项目**：优先使用 **OpenTelemetry**（本项目方案）
   - 开放标准，不绑定特定后端
   - 社区活跃，持续演进
   - 可切换不同的后端（Jaeger、Zipkin、Tempo、DataDog 等）

2. **简单场景**：手动传播 TraceID
   - 只需要日志关联，不需要完整 APM
   - 团队规模小，维护成本可控

3. **云环境**：使用云厂商 SDK
   - 深度集成云服务
   - 开箱即用的监控面板

4. **商业项目**：APM 工具
   - 完整的监控、告警、分析功能
   - 专业技术支持

## 生产环境建议

- 调整采样率：修改 `TraceIDRatioBased(0.01)` 参数
- 替换 exporter：从 `stdout` 改为 OTLP（发送到 Jaeger/Tempo 等）
- 添加错误和慢请求的高优先级采样
- 配置日志输出到文件或日志系统
