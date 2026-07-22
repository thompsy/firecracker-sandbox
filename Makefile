SHELL := /bin/bash
ID ?= 0

.PHONY: setup deps initramfs build net-up net-down run detach stop list clean distclean

setup: deps initramfs build  ## fetch binaries, build the initramfs + firevm (run once)

deps:                   ## download firecracker, kernel, busybox
	@scripts/deps.sh

initramfs:              ## build vm/initramfs.cpio from busybox + initramfs/init
	@scripts/build-initramfs.sh

build:                  ## build the firevm CLI -> bin/firevm
	@go build -o bin/firevm ./cmd/firevm

net-up:                 ## create the shared VM bridge fc-br0 (sudo)
	@scripts/host-net.sh up

net-down:               ## remove the VM bridge (sudo)
	@scripts/host-net.sh down

run: build              ## boot a VM on the console:   make run ID=0
	@sudo bin/firevm run $(ID)

detach: build           ## boot a VM in the background: make detach ID=1
	@sudo bin/firevm detach $(ID)

stop:                   ## stop a backgrounded VM:      make stop ID=1
	@sudo bin/firevm stop $(ID)

list:                   ## list running VMs
	@bin/firevm list

clean:                  ## stop VMs, remove taps + bridge, clear run files (sudo)
	@scripts/clean.sh

distclean: clean        ## also remove downloaded binaries, kernel, initramfs
	@rm -rf bin/* vm/*
