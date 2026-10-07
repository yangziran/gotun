package tunnel

import "sync"

// 默认 32KB 对流媒体（如 YouTube）不够理想
// 我们将 io.Copy 的默认缓冲区提升至 128KB，极大提升高带宽大吞吐场景的性能
const bufSize = 128 * 1024

var bufPool = sync.Pool{
	New: func() any {
		buf := make([]byte, bufSize)
		return &buf
	},
}

// getBuffer 从全局内存池获取一个 128KB 的缓冲数组
func getBuffer() []byte {
	return *(bufPool.Get().(*[]byte))
}

// putBuffer 将使用完毕的缓冲区放回内存池，降低 GC 压力
func putBuffer(buf []byte) {
	bufPool.Put(&buf)
}
