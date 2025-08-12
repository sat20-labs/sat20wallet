package common

import "errors"


var (
	ErrKeyNotFound = errors.New("Key not found")
)

type ReadBatch interface {
	Get(key []byte) ([]byte, error) // 与 KVDB.Read 区分
}

type WriteBatch interface {
	Put(key, value []byte) error
	Delete(key []byte) error
	Flush() error
	Close()
}

// 每个调用都是完整的transaction
type KVDB interface {

	DropAll() error
	DropPrefix([]byte) error

	Read(key []byte) ([]byte, error)
	Write(key, value []byte) error
	Delete(key []byte) error
	Close() error


	NewWriteBatch() WriteBatch
	
	// 遍历读
	BatchRead(prefix []byte, reverse bool, r func(k, v []byte) error) error
	BatchReadV2(prefix, seekKey []byte, reverse bool, r func(k, v []byte) error) error  // 只用于非客户端模式下

	// 随机读
	View(func(ReadBatch) error) error
}

