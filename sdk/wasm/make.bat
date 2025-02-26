set GOOS=js
set GOARCH=wasm
go build -o html/wasm/sat20wallet.wasm -ldflags="-s -w" main.go