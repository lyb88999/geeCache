package geeCache

type PeerPicker interface {
	// PickPeer 根据传入的key选择相应节点PeerGetter
	PickPeer(key string) (peer PeerGetter, ok bool)
}

type PeerGetter interface {
	// Get 从对应的group查找缓存值
	Get(group string, key string) ([]byte, error)
}
