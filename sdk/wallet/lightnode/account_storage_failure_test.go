//go:build js && wasm

package lightnode

import (
	"errors"
	"reflect"
	"strings"
	"syscall/js"
	"testing"

	"github.com/sat20-labs/indexer/common"
)

// Run in a real browser through the existing PWA acceptance runner. Faults
// intercept native IndexedDB operations; the database and rollback are real.
func storageReviewDB(t *testing.T) common.KVDB {
	t.Helper()
	database := NewKVDB()
	if database == nil {
		t.Fatal("IndexedDB is unavailable")
	}
	t.Cleanup(func() { database.Close() })
	if err := database.DropAll(); err != nil {
		t.Fatal(err)
	}
	return database
}

func storageReviewFault(t *testing.T, script string) {
	t.Helper()
	js.Global().Call("eval", script)
	t.Cleanup(func() {
		js.Global().Call("eval", `globalThis.restoreStorageFault(); delete globalThis.restoreStorageFault`)
	})
}

func TestAccountBrowserStorageReview(t *testing.T) {
	// Even with extension APIs present, both environments must use IndexedDB.
	previousChrome := js.Global().Get("chrome")
	js.Global().Call("eval", `globalThis.storageReviewDescriptor = Object.getOwnPropertyDescriptor(globalThis, "localStorage")`)
	js.Global().Call("eval", `const deny=()=>{throw new Error('legacy storage accessed')};globalThis.chrome={storage:{local:{get:deny,set:deny,remove:deny}}};Object.defineProperty(globalThis,'localStorage',{configurable:true,value:{getItem:deny,setItem:deny,removeItem:deny}})`)
	defer js.Global().Set("chrome", previousChrome)
	defer js.Global().Call("eval", `Object.defineProperty(globalThis,"localStorage",globalThis.storageReviewDescriptor); delete globalThis.storageReviewDescriptor`)
	t.Run("MissingKey", func(t *testing.T) {
		database := storageReviewDB(t)
		_, err := database.Read([]byte("profile"))
		if !errors.Is(err, common.ErrKeyNotFound) {
			t.Fatalf("missing key: %v", err)
		}
	})
	for _, op := range []string{"write", "delete", "batch"} {
		t.Run("Failure/"+op, func(t *testing.T) {
			database := storageReviewDB(t)
			if err := database.Write([]byte("profile"), []byte("old")); err != nil {
				t.Fatal(err)
			}
			storageReviewFault(t, `const proto=IDBObjectStore.prototype;const put=proto.put,del=proto.delete;proto.put=function(){throw new DOMException('injected write failure','QuotaExceededError')};proto.delete=function(){throw new DOMException('injected write failure','UnknownError')};globalThis.restoreStorageFault=()=>{proto.put=put;proto.delete=del}`)
			var err error
			switch op {
			case "write":
				err = database.Write([]byte("profile"), []byte("new"))
			case "delete":
				err = database.Delete([]byte("profile"))
			case "batch":
				batch := database.NewWriteBatch()
				defer batch.Close()
				if e := batch.Put([]byte("profile"), []byte("new")); e != nil {
					t.Fatal(e)
				}
				err = batch.Flush()
			}
			if err == nil || !strings.Contains(err.Error(), "injected write failure") {
				t.Fatalf("storage error swallowed: %v", err)
			}
			value, e := database.Read([]byte("profile"))
			if e != nil || string(value) != "old" {
				t.Fatalf("failed operation changed profile: %q %v", value, e)
			}
		})
	}
	t.Run("AtomicBatch", func(t *testing.T) {
		database := storageReviewDB(t)
		for key, value := range map[string]string{"wallet": "old-wallet", "profile": "old-profile", "selected": "old-selected"} {
			if err := database.Write([]byte(key), []byte(value)); err != nil {
				t.Fatal(err)
			}
		}
		storageReviewFault(t, `const proto=IDBObjectStore.prototype;const put=proto.put;let calls=0;proto.put=function(...args){if(++calls===2)throw new DOMException('injected quota failure','QuotaExceededError');return put.apply(this,args)};globalThis.restoreStorageFault=()=>{proto.put=put}`)
		batch := database.NewWriteBatch()
		defer batch.Close()
		_ = batch.Delete([]byte("selected"))
		_ = batch.Put([]byte("wallet"), []byte("new-wallet"))
		_ = batch.Put([]byte("profile"), []byte("new-profile"))
		if err := batch.Flush(); err == nil {
			t.Fatal("failed batch returned success")
		}
		for key, want := range map[string]string{"wallet": "old-wallet", "profile": "old-profile", "selected": "old-selected"} {
			value, err := database.Read([]byte(key))
			if err != nil || string(value) != want {
				t.Fatalf("partial batch persisted %s=%q, err=%v", key, value, err)
			}
		}
	})
}

func TestIndexedDBTransactionBoundaries(t *testing.T) {
	t.Run("AsyncRequestFailureRollsBackBatch", func(t *testing.T) {
		database := storageReviewDB(t)
		for key, value := range map[string]string{"wallet": "old-wallet", "profile": "old-profile", "selected": "old-selected"} {
			if err := database.Write([]byte(key), []byte(value)); err != nil {
				t.Fatal(err)
			}
		}
		// The second request is a native duplicate-key add. It fails
		// asynchronously after earlier writes/deletes have been processed.
		storageReviewFault(t, `const proto=IDBObjectStore.prototype;const put=proto.put;let calls=0;proto.put=function(...args){return ++calls===2?this.add(...args):put.apply(this,args)};globalThis.restoreStorageFault=()=>{proto.put=put}`)
		batch := database.NewWriteBatch()
		defer batch.Close()
		_ = batch.Delete([]byte("selected"))
		_ = batch.Put([]byte("wallet"), []byte("new-wallet"))
		_ = batch.Put([]byte("profile"), []byte("new-profile"))
		if err := batch.Flush(); err == nil || !strings.Contains(err.Error(), "ConstraintError") {
			t.Fatalf("native request failure was swallowed: %v", err)
		}
		for key, want := range map[string]string{"wallet": "old-wallet", "profile": "old-profile", "selected": "old-selected"} {
			value, err := database.Read([]byte(key))
			if err != nil || string(value) != want {
				t.Fatalf("async failure partially persisted %s=%q: %v", key, value, err)
			}
		}
	})
	t.Run("AsyncOpenFailure", func(t *testing.T) {
		storageReviewFault(t, `const proto=IDBFactory.prototype;const open=proto.open;proto.open=function(name,version){const req=open.call(this,name+'-aborted-open',version);req.addEventListener('upgradeneeded',()=>req.transaction.abort());return req};globalThis.restoreStorageFault=()=>{proto.open=open}`)
		database := NewKVDB()
		if database == nil {
			t.Fatal("missing database")
		}
		defer database.Close()
		_, err := database.Read([]byte("profile"))
		if err == nil || errors.Is(err, common.ErrKeyNotFound) {
			t.Fatalf("asynchronous open failure mistaken for absence: %v", err)
		}
	})
	t.Run("AbortAfterRequestSuccess", func(t *testing.T) {
		database := storageReviewDB(t)
		if err := database.Write([]byte("profile"), []byte("old")); err != nil {
			t.Fatal(err)
		}
		storageReviewFault(t, `const proto=IDBObjectStore.prototype;const put=proto.put;proto.put=function(...args){const req=put.apply(this,args);req.addEventListener('success',()=>this.transaction.abort());return req};globalThis.restoreStorageFault=()=>{proto.put=put}`)
		if err := database.Write([]byte("profile"), []byte("new")); err == nil {
			t.Fatal("request success was reported before transaction abort")
		}
		value, err := database.Read([]byte("profile"))
		if err != nil || string(value) != "old" {
			t.Fatalf("aborted transaction committed: %q %v", value, err)
		}
	})
	t.Run("ReadAndScanFailure", func(t *testing.T) {
		database := storageReviewDB(t)
		storageReviewFault(t, `const proto=IDBObjectStore.prototype;const get=proto.get,cursor=proto.openCursor;proto.get=function(){throw new DOMException('injected read failure','UnknownError')};proto.openCursor=function(){throw new DOMException('injected read failure','UnknownError')};globalThis.restoreStorageFault=()=>{proto.get=get;proto.openCursor=cursor}`)
		_, err := database.Read([]byte("profile"))
		if err == nil || errors.Is(err, common.ErrKeyNotFound) {
			t.Fatalf("I/O error mistaken for missing key: %v", err)
		}
		if err := database.BatchRead(nil, false, func(_, _ []byte) error { return nil }); err == nil {
			t.Fatal("scan error swallowed")
		}
	})
	t.Run("OpenFailure", func(t *testing.T) {
		storageReviewFault(t, `const proto=IDBFactory.prototype;const open=proto.open;proto.open=function(){throw new DOMException('injected open failure','SecurityError')};globalThis.restoreStorageFault=()=>{proto.open=open}`)
		database := NewKVDB()
		if database == nil {
			t.Fatal("missing database")
		}
		defer database.Close()
		_, err := database.Read([]byte("profile"))
		if err == nil || !strings.Contains(err.Error(), "injected open failure") {
			t.Fatalf("open error swallowed: %v", err)
		}
	})
	t.Run("PersistentReopenAndSnapshot", func(t *testing.T) {
		database := storageReviewDB(t)
		batch := database.NewWriteBatch()
		defer batch.Close()
		_ = batch.Put([]byte("a-1"), []byte{0, 255, 3})
		_ = batch.Put([]byte("a-2"), []byte("old"))
		_ = batch.Put([]byte("b-1"), []byte("other"))
		if err := batch.Flush(); err != nil {
			t.Fatal(err)
		}
		if err := database.Close(); err != nil {
			t.Fatal(err)
		}
		database = NewKVDB()
		defer database.Close()
		err := database.View(func(snapshot common.ReadBatch) error {
			value, err := snapshot.Get([]byte("a-1"))
			if err != nil || !reflect.DeepEqual(value, []byte{0, 255, 3}) {
				t.Fatalf("binary reopen: %v %v", value, err)
			}
			value[0] = 99
			again, _ := snapshot.Get([]byte("a-1"))
			if again[0] != 0 {
				t.Fatal("Get did not copy")
			}
			if err := database.Write([]byte("a-2"), []byte("new")); err != nil {
				return err
			}
			old, err := snapshot.Get([]byte("a-2"))
			if err != nil || string(old) != "old" {
				t.Fatalf("View mixed revisions: %q %v", old, err)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		var keys []string
		err = database.Scan(common.ScanOptions{Prefix: []byte("a-"), Reverse: true, Limit: 1}, func(key, value []byte) error { keys = append(keys, string(key)); return nil })
		if err != nil || !reflect.DeepEqual(keys, []string{"a-2"}) {
			t.Fatalf("reverse scan: %v %v", keys, err)
		}
		if err := database.DropPrefix([]byte("a-")); err != nil {
			t.Fatal(err)
		}
		if _, err := database.Read([]byte("a-1")); !errors.Is(err, common.ErrKeyNotFound) {
			t.Fatal(err)
		}
		if value, err := database.Read([]byte("b-1")); err != nil || string(value) != "other" {
			t.Fatalf("DropPrefix affected other keys: %q %v", value, err)
		}
	})
	t.Run("BatchLastOperationWins", func(t *testing.T) {
		database := storageReviewDB(t)
		batch := database.NewWriteBatch()
		defer batch.Close()
		_ = batch.Put([]byte("deleted"), []byte("value"))
		_ = batch.Delete([]byte("deleted"))
		_ = batch.Delete([]byte("kept"))
		_ = batch.Put([]byte("kept"), []byte("value"))
		if err := batch.Flush(); err != nil {
			t.Fatal(err)
		}
		if _, err := database.Read([]byte("deleted")); !errors.Is(err, common.ErrKeyNotFound) {
			t.Fatal(err)
		}
		if value, err := database.Read([]byte("kept")); err != nil || string(value) != "value" {
			t.Fatalf("Put/Delete order: %q %v", value, err)
		}
	})
}
