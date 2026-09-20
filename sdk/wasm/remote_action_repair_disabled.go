//go:build js && wasm && !remoteactionrepair

package main

import "syscall/js"

func registerRemoteActionRepair(js.Value) {}
