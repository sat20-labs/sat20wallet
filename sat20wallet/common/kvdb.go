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

	NewBatchWrite() WriteBatch
	BatchRead(prefix []byte, r func(k, v []byte) error) error
}

