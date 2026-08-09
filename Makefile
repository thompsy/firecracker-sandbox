SHELL := /bin/bash
ID ?= 0

.PHONY: setup deps initramfs build bpf net-up net-down daemon run stop list flowmon e2e clean distclean

## fetch binaries, build initramfs, eBPF objects, and CLIs
setup: deps initramfs bpf build


## download firecracker, kernel, busybox
deps:
	@scripts/deps.sh

## build vm/initramfs.cpio from busybox and initramfs/init
initramfs:
	@scripts/build-initramfs.sh

## build the CLIs -> bin/firevm, bin/firevmd, bin/flowmon
build:
	@go build -o bin/firevm ./cmd/firevm
	@go build -o bin/firevmd ./cmd/firevmd
	@go build -o bin/flowmon ./cmd/flowmon

## regenerate the eBPF objects from flow/*.c (requires clang)
bpf:
	@go generate ./flow

## create the shared VM bridge fc-br0 (requires sudo)
net-up:
	@scripts/host-net.sh up

## remove the VM bridge (requires sudo)
net-down:
	@scripts/host-net.sh down

## run the control-plane daemon in the foreground (requires sudo)
daemon: build
	@sudo bin/firevmd

## launch a VM in the background: make run ID=0
run: build
	@sudo bin/firevm run $(ID)

## stop a running VM: make stop ID=0
stop:
	@sudo bin/firevm stop $(ID)

## list running VMs
list:
	@sudo bin/firevm list

## attach the eBPF flow monitor to a VM: make flowmon ID=0
flowmon: build
	@sudo bin/flowmon $(ID)

## end-to-end smoke test: two VMs talk, assert the flow shows in /stats (sudo)
e2e: build
	@sudo scripts/e2e_test.sh

## stop VMs, remove taps and bridge, clear run files (requires sudo)
clean:
	@scripts/clean.sh

## also remove downloaded binaries, kernel, initramfs
distclean: clean
	@rm -rf bin/* vm/*
