package metrics

import (
	"io"
	"net/http"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/yangziran/gotun/pkg/logger"
)

var (
	ActiveConnections = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gotun_active_connections",
			Help: "当前活跃的隧道流量连接数",
		},
		[]string{"tunnel_name", "type"},
	)

	BytesTransferred = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gotun_bytes_transferred_total",
			Help: "总传输字节数",
		},
		[]string{"tunnel_name", "direction"}, // 流量方向: "up" (上行) 或 "down" (下行)
	)

	SSHReconnects = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gotun_ssh_reconnects_total",
			Help: "SSH 服务器底层掉线重连总次数",
		},
		[]string{"server_name"},
	)
)

var (
	serverMu      sync.Mutex
	effectiveAddr string
)

// StartMetricsServer 启动一个后台 HTTP 服务器来提供 prometheus 指标。
// 监听地址不支持热加载切换：首次启动的地址生效，后续重载传入不同地址会被忽略并告警。
func StartMetricsServer(addr string) {
	if addr == "" {
		return
	}
	serverMu.Lock()
	defer serverMu.Unlock()
	if effectiveAddr != "" {
		if addr != effectiveAddr {
			logger.Warn("metrics_addr 不支持热加载切换，已忽略新地址，修改后需重启进程生效", "current", effectiveAddr, "ignored", addr)
		}
		return
	}
	effectiveAddr = addr
	go func() {
		mux := http.NewServeMux()
		mux.Handle("/metrics", promhttp.Handler())
		logger.Info("启动指标监控服务 (Prometheus)", "addr", addr)
		if err := http.ListenAndServe(addr, mux); err != nil {
			logger.Error("指标监控服务运行异常", "err", err)
		}
	}()
}

type trackingWriter struct {
	w       io.Writer
	counter prometheus.Counter
}

func (tw *trackingWriter) Write(p []byte) (n int, err error) {
	n, err = tw.w.Write(p)
	if n > 0 {
		tw.counter.Add(float64(n))
	}
	return
}

// NewTrackingWriter 封装一个 io.Writer，用于统计向其写入的字节数
func NewTrackingWriter(w io.Writer, tunnelName string, direction string) io.Writer {
	return &trackingWriter{
		w:       w,
		counter: BytesTransferred.WithLabelValues(tunnelName, direction),
	}
}
