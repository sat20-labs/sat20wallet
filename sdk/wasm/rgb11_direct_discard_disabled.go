//go:build js && wasm && !rgb11discard

package main

import "syscall/js"

func registerRGB11Discard(js.Value) {}
