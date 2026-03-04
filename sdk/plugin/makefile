

.PHONY: all clean

all:
	rm -f ./wallet.so
	rm -f ../../satoshinet/wallet.so
	# 调试时使用下面命令重新编译插件
	go build -buildmode=plugin -o wallet.so -gcflags="all=-N -l" main.go

	# 发布时用下面指令编译
	# go build -buildmode=plugin -o wallet.so -ldflags="-s -w" main.go
	cp wallet.so ../../satoshinet/.
