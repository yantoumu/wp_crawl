package monitor

import (
	"runtime"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
)

// MemoryMonitor 内存监控器
type MemoryMonitor struct {
	logger          *zap.Logger
	maxMemoryMB     int64
	warningThreshold float64  // 警告阈值（百分比）
	criticalThreshold float64 // 严重阈值（百分比）

	// 统计信息
	peakMemoryMB    atomic.Int64
	gcCount         atomic.Int64
	forceGCCount    atomic.Int64

	// 控制
	stopChan        chan struct{}
	checkInterval   time.Duration
}

// NewMemoryMonitor 创建内存监控器
func NewMemoryMonitor(maxMemoryMB int, logger *zap.Logger) *MemoryMonitor {
	return &MemoryMonitor{
		logger:            logger,
		maxMemoryMB:       int64(maxMemoryMB),
		warningThreshold:  0.75,  // 75% 警告
		criticalThreshold: 0.85,  // 85% 严重
		stopChan:          make(chan struct{}),
		checkInterval:     30 * time.Second, // 每 30 秒检查一次
	}
}

// Start 启动内存监控
func (m *MemoryMonitor) Start() {
	go m.monitorLoop()
	m.logger.Info("Memory monitor started",
		zap.Int64("max_memory_mb", m.maxMemoryMB),
		zap.Float64("warning_threshold", m.warningThreshold),
		zap.Float64("critical_threshold", m.criticalThreshold))
}

// Stop 停止内存监控
func (m *MemoryMonitor) Stop() {
	close(m.stopChan)
	m.logger.Info("Memory monitor stopped")
}

// monitorLoop 监控循环
func (m *MemoryMonitor) monitorLoop() {
	ticker := time.NewTicker(m.checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			m.checkMemory()
		case <-m.stopChan:
			return
		}
	}
}

// checkMemory 检查内存使用情况
func (m *MemoryMonitor) checkMemory() {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)

	// 当前内存使用（MB）
	currentMB := int64(ms.Alloc / 1024 / 1024)
	heapMB := int64(ms.HeapAlloc / 1024 / 1024)
	sysMB := int64(ms.Sys / 1024 / 1024)

	// 更新峰值
	if currentMB > m.peakMemoryMB.Load() {
		m.peakMemoryMB.Store(currentMB)
	}

	// 计算使用率
	usagePercent := float64(currentMB) / float64(m.maxMemoryMB)

	// 记录GC次数
	m.gcCount.Store(int64(ms.NumGC))

	// 定期日志（正常情况）
	if usagePercent < m.warningThreshold {
		m.logger.Debug("Memory status",
			zap.Int64("alloc_mb", currentMB),
			zap.Int64("heap_mb", heapMB),
			zap.Int64("sys_mb", sysMB),
			zap.Float64("usage_percent", usagePercent*100),
			zap.Uint32("num_gc", ms.NumGC))
		return
	}

	// 警告级别
	if usagePercent >= m.warningThreshold && usagePercent < m.criticalThreshold {
		m.logger.Warn("Memory usage warning",
			zap.Int64("alloc_mb", currentMB),
			zap.Int64("max_mb", m.maxMemoryMB),
			zap.Float64("usage_percent", usagePercent*100),
			zap.String("action", "monitoring"))
		return
	}

	// 严重级别 - 触发 GC
	if usagePercent >= m.criticalThreshold {
		m.logger.Error("Memory usage critical - forcing GC",
			zap.Int64("alloc_mb", currentMB),
			zap.Int64("max_mb", m.maxMemoryMB),
			zap.Float64("usage_percent", usagePercent*100))

		// 强制触发 GC
		m.ForceGC()

		// GC 后再次检查
		runtime.ReadMemStats(&ms)
		afterMB := int64(ms.Alloc / 1024 / 1024)
		freedMB := currentMB - afterMB

		m.logger.Info("GC completed",
			zap.Int64("before_mb", currentMB),
			zap.Int64("after_mb", afterMB),
			zap.Int64("freed_mb", freedMB),
			zap.Float64("new_usage_percent", float64(afterMB)/float64(m.maxMemoryMB)*100))
	}
}

// ForceGC 强制执行垃圾回收
func (m *MemoryMonitor) ForceGC() {
	runtime.GC()
	m.forceGCCount.Add(1)
}

// GetStats 获取监控统计信息
func (m *MemoryMonitor) GetStats() map[string]int64 {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)

	return map[string]int64{
		"current_mb":     int64(ms.Alloc / 1024 / 1024),
		"heap_mb":        int64(ms.HeapAlloc / 1024 / 1024),
		"sys_mb":         int64(ms.Sys / 1024 / 1024),
		"peak_mb":        m.peakMemoryMB.Load(),
		"gc_count":       m.gcCount.Load(),
		"force_gc_count": m.forceGCCount.Load(),
		"num_goroutine":  int64(runtime.NumGoroutine()),
	}
}

// SetCheckInterval 设置检查间隔
func (m *MemoryMonitor) SetCheckInterval(interval time.Duration) {
	m.checkInterval = interval
}

// SetThresholds 设置阈值
func (m *MemoryMonitor) SetThresholds(warning, critical float64) {
	m.warningThreshold = warning
	m.criticalThreshold = critical
}
