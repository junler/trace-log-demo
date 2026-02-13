package main

import (
	"context"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	stdout "go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var (
	tracer = otel.Tracer("gin-server")
	logger *zap.Logger
)

func main() {
	// Initialize logger
	var err error
	logger, err = initLogger()
	if err != nil {
		fmt.Printf("Failed to initialize logger: %v\n", err)
		os.Exit(1)
	}
	defer logger.Sync()

	tp, err := initTracer()
	if err != nil {
		logger.Fatal("Failed to initialize tracer", zap.Error(err))
	}
	defer func() {
		if err := tp.Shutdown(context.Background()); err != nil {
			logger.Error("Error shutting down tracer provider", zap.Error(err))
		}
	}()
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(otelgin.Middleware("my-server"))
	r.Use(loggerWithTraceID())
	tmplName := "user"
	tmplStr := "user {{ .name }} (id {{ .id }})\n"
	tmpl := template.Must(template.New(tmplName).Parse(tmplStr))
	r.SetHTMLTemplate(tmpl)
	r.GET("/users/:id", func(c *gin.Context) {
		id := c.Param("id")
		name := getUser(c, id)
		otelgin.HTML(c, http.StatusOK, tmplName, gin.H{
			"name": name,
			"id":   id,
		})
	})
	logger.Info("Service A starting on :8080")
	_ = r.Run(":8080")
}

// initLogger initializes zap logger based on environment
// dev: console output, prod/test: file output
func initLogger() (*zap.Logger, error) {
	env := os.Getenv("ENV")
	if env == "" {
		env = "dev"
	}

	var config zap.Config

	if env == "dev" {
		// Development: console output with colored level
		config = zap.NewDevelopmentConfig()
		config.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
		config.EncoderConfig.EncodeTime = zapcore.TimeEncoderOfLayout("2006-01-02 15:04:05")
	} else {
		// Production/Test: file output with JSON format
		config = zap.NewProductionConfig()
		config.EncoderConfig.EncodeTime = zapcore.TimeEncoderOfLayout("2006-01-02 15:04:05")
		config.OutputPaths = []string{
			fmt.Sprintf("logs/service-a-%s.log", time.Now().Format("2006-01-02")),
			"stdout",
		}
		config.ErrorOutputPaths = []string{
			fmt.Sprintf("logs/service-a-error-%s.log", time.Now().Format("2006-01-02")),
			"stderr",
		}
	}

	return config.Build()
}

func initTracer() (*sdktrace.TracerProvider, error) {
	exporter, err := stdout.New(stdout.WithPrettyPrint())
	if err != nil {
		return nil, err
	}
	sampler := sdktrace.ParentBased(sdktrace.TraceIDRatioBased(0.01))
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sampler),
		sdktrace.WithBatcher(exporter),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	return tp, nil
}

func getUser(c *gin.Context, id string) string {
	// Pass the built-in `context.Context` object from http.Request to OpenTelemetry APIs
	// where required. It is available from gin.Context.Request.Context()
	ctx, span := tracer.Start(c.Request.Context(), "getUser", oteltrace.WithAttributes(attribute.String("id", id)))
	defer span.End()
	traceID := span.SpanContext().TraceID().String()

	logInfo(traceID, "UserService", fmt.Sprintf("Getting user with id=%s", id))

	// Call service-b
	client := &http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)}
	url := "http://localhost:8081/info"
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)

	logInfo(traceID, "UserService", fmt.Sprintf("Calling service-b: GET %s", url))

	resp, err := client.Do(req)
	if err != nil {
		logError(traceID, "UserService", fmt.Sprintf("Failed to call service-b: %v", err))
	} else {
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		logInfo(traceID, "UserService", fmt.Sprintf("Service-b response: status=%d, body=%s", resp.StatusCode, string(body)))
	}

	var result string
	if id == "123" {
		result = "otelgin tester"
	} else {
		result = "unknown"
	}

	logInfo(traceID, "UserService", fmt.Sprintf("Returning user: %s", result))
	return result
}

// loggerWithTraceID - custom logger middleware that includes trace_id
func loggerWithTraceID() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		raw := c.Request.URL.RawQuery
		method := c.Request.Method

		if raw != "" {
			path = path + "?" + raw
		}

		c.Next()

		traceID := oteltrace.SpanFromContext(c.Request.Context()).SpanContext().TraceID().String()
		latency := time.Since(start)
		clientIP := c.ClientIP()
		statusCode := c.Writer.Status()

		fields := []zap.Field{
			zap.String("trace_id", traceID),
			zap.String("method", method),
			zap.String("path", path),
			zap.Int("status", statusCode),
			zap.Duration("latency", latency),
			zap.String("client_ip", clientIP),
		}

		if statusCode >= 500 {
			logger.Error("HTTP Request", fields...)
		} else if statusCode >= 400 {
			logger.Warn("HTTP Request", fields...)
		} else {
			logger.Info("HTTP Request", fields...)
		}
	}
}

// logInfo logs an info message with trace ID
func logInfo(traceID, service, message string) {
	logger.Info(message,
		zap.String("trace_id", traceID),
		zap.String("service", service),
	)
}

// logError logs an error message with trace ID
func logError(traceID, service, message string) {
	logger.Error(message,
		zap.String("trace_id", traceID),
		zap.String("service", service),
	)
}
