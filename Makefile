# Keybox Lite - build helpers

SHELL := /bin/sh
VERSION := $(shell sed -n 's/^version=//p' module.prop)

.PHONY: all build webui package clean distclean help

all: package

build:
	sh build.sh

webui:
	@[ -d webui/node_modules ] || ( echo 'installing webui deps...'; cd webui && npm install --no-audit --no-fund --registry=https://registry.npmmirror.com )
	cd webui && npm run build

package: build webui
	sh package.sh

clean:
	rm -rf dist webroot bin/keyboxlite bin/keyboxlite.upx icon.png

distclean: clean
	rm -rf webui/node_modules

help:
	@echo 'Keybox Lite targets:'
	@echo '  make build      Go binary (arm64 static) + UPX'
	@echo '  make webui      build the Svelte WebUI into webroot/'
	@echo '  make package    build everything -> dist/keybox-lite-$(VERSION).zip'
	@echo '  make clean      remove build outputs'
	@echo '  make distclean  also remove webui/node_modules'
