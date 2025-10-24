package domain

import (
	"hash/fnv"
	"sync"
)

// ShardedMap 分片 map，减少锁竞争
type ShardedMap struct {
	shards []*MapShard
	count  int
}

// MapShard 单个分片
type MapShard struct {
	mu   sync.RWMutex
	data map[string]string
}

// NewShardedMap 创建分片 map
func NewShardedMap(shardCount int) *ShardedMap {
	sm := &ShardedMap{
		shards: make([]*MapShard, shardCount),
		count:  shardCount,
	}

	for i := 0; i < shardCount; i++ {
		sm.shards[i] = &MapShard{
			data: make(map[string]string),
		}
	}

	return sm
}

// getShard 根据 key 获取对应的分片
func (sm *ShardedMap) getShard(key string) *MapShard {
	h := fnv.New32a()
	h.Write([]byte(key))
	index := h.Sum32() % uint32(sm.count)
	return sm.shards[index]
}

// Set 设置键值对
func (sm *ShardedMap) Set(key, value string) {
	shard := sm.getShard(key)
	shard.mu.Lock()
	shard.data[key] = value
	shard.mu.Unlock()
}

// Get 获取值
func (sm *ShardedMap) Get(key string) (string, bool) {
	shard := sm.getShard(key)
	shard.mu.RLock()
	value, exists := shard.data[key]
	shard.mu.RUnlock()
	return value, exists
}

// GetAll 获取所有键值对（用于持久化）
func (sm *ShardedMap) GetAll() map[string]string {
	result := make(map[string]string)

	// 并发读取所有分片
	var wg sync.WaitGroup
	var mu sync.Mutex

	for i := range sm.shards {
		wg.Add(1)
		go func(shard *MapShard) {
			defer wg.Done()

			shard.mu.RLock()
			localCopy := make(map[string]string, len(shard.data))
			for k, v := range shard.data {
				localCopy[k] = v
			}
			shard.mu.RUnlock()

			mu.Lock()
			for k, v := range localCopy {
				result[k] = v
			}
			mu.Unlock()
		}(sm.shards[i])
	}

	wg.Wait()
	return result
}

// Count 返回总条目数
func (sm *ShardedMap) Count() int {
	total := 0
	for _, shard := range sm.shards {
		shard.mu.RLock()
		total += len(shard.data)
		shard.mu.RUnlock()
	}
	return total
}

// Clear 清空所有数据（用于内存释放）
func (sm *ShardedMap) Clear() {
	for _, shard := range sm.shards {
		shard.mu.Lock()
		shard.data = make(map[string]string)
		shard.mu.Unlock()
	}
}
