.PHONY: all clean

all:
# ifeq ($(shell uname), Darwin)
# 	if ! brew ls --versions tinygo > /dev/null; then \
# 		brew tap tinygo-org/tools && brew install tinygo; \
# 	fi
# else ifeq ($(shell uname), Linux)
# 	if ! command -v tinygo > /dev/null; then \
# 		sudo add-apt-repository ppa:tinygo-team/ppa; \
# 		sudo apt update; \
# 		sudo apt install tinygo; \
# 	fi
# 	if ! command -v wasm-opt > /dev/null; then \
# 		sudo apt install binaryen; \
# 	fi
# else
# 	echo "Unsupported operating system"
# endif
	GOOS=js GOARCH=wasm go build -o html/wasm/sat20wallet.wasm -ldflags="-s -w" main.go
# wasm-opt -Oz html/wasm/sat20wallet.wasm -o html/wasm/sat20wallet.wasm --enable-bulk-memory 
# tinygo build -o out.wasm -target wasm -no-debug main.go

# rm out.wasm

# clean:
# 	rm -f out.wasm html/wasm/sat20wallet.wasm