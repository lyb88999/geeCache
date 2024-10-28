package singleFlight

import "sync"

// call 代表正在进行中, 或者已经结束的请求, 使用sync.WaitGroup锁避免重入
type call struct {
	wg  sync.WaitGroup
	val any
	err error
}

// Group 是singleflight的主要数据结构, 管理不同key的请求(call)
type Group struct {
	mu sync.Mutex // 防止m被并发读写而加上的锁
	m  map[string]*call
}

// Do 接收两个参数, 第一个参数是key, 第二个参数是fn
// 作用: 针对相同的key, 无论Do被调用多少次, 函数fn只会被调用一次, 等fn调用结束了返回值和错误
func (g *Group) Do(key string, fn func() (interface{}, error)) (interface{}, error) {
	g.mu.Lock()
	if g.m == nil {
		g.m = make(map[string]*call)
	}
	if c, ok := g.m[key]; ok {
		g.mu.Unlock()
		c.wg.Wait()
		return c.val, c.err
	}
	c := new(call)
	c.wg.Add(1)
	g.m[key] = c
	g.mu.Unlock()
	c.val, c.err = fn()
	c.wg.Done()
	g.mu.Lock()
	delete(g.m, key)
	g.mu.Unlock()
	return c.val, c.err
}
