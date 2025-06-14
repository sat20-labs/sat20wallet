package common

type WriteBatch interface {
	Put(key, value []byte) error
	Remove(key []byte) error
	Flush() error
	Close()
}

// 每个调用都是完整的transaction
type KVDB interface {
	Read(key []byte) ([]byte, error)
	Write(key, value []byte) error
	Delete(key []byte) error
	Close() error

	NewBatchWrite() WriteBatch
	BatchRead(prefix []byte, reverse bool, r func(k, v []byte) error) error
	BatchReadV2(prefix, seekKey []byte, reverse bool, r func(k, v []byte) error) error  // 只用于非客户端模式下
}

