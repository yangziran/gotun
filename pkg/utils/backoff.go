package utils

import (
	"time"
)

// Backoff 简单的指数退避算法实现
type Backoff struct {
	Min     time.Duration
	Max     time.Duration
	Factor  float64
	current time.Duration
}

// NewBackoff 创建一个新的退避计算器
func NewBackoff(min, max time.Duration, factor float64) *Backoff {
	return &Backoff{
		Min:     min,
		Max:     max,
		Factor:  factor,
		current: min,
	}
}

// Duration 获取当前应该休眠的时间，并将内部状态递增
func (b *Backoff) Duration() time.Duration {
	ret := b.current
	next := time.Duration(float64(b.current) * b.Factor)
	if next > b.Max {
		next = b.Max
	}
	b.current = next
	return ret
}

// Reset 重置退避时间到最小值（通常在重连成功后调用）
func (b *Backoff) Reset() {
	b.current = b.Min
}
