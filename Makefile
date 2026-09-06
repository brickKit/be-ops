# be-ops 不是 brickKit 组件，但仍按总纲 §I 的 9 个门禁目标写。
.PHONY: check-version test image migrate-idempotent dag-check contract-check \
        import-scan smoke module-check all

check-version:
	@echo "N/A：非组件仓库，没有 component.yaml"

test:
	go test ./... -race

image:
	docker build -t brickkit/be-ops:dev .

migrate-idempotent:
	@echo "N/A：非组件仓库，没有迁移"

dag-check:
	@go list ./... >/dev/null && echo "✓ 包依赖图无环（Go 编译器本身就不允许循环 import）"

contract-check:
	@echo "N/A：非组件仓库，没有 contracts/"

import-scan:
	@bad="$$(go list -deps ./... 2>/dev/null | grep '^github.com/brickKit/' | grep -vE '^github.com/brickKit/be-ops($$|/)' | grep -vE '^github.com/brickKit/be-sdk-go($$|/)')"; \
	if [ -n "$$bad" ]; then \
		echo "✗ be-ops 不许依赖任何组件仓库：$$bad"; exit 1; \
	fi; \
	echo "✓ 零组件依赖（be-sdk-go 例外，本工具用不到但白名单允许）"

smoke:
	@echo "N/A：非组件仓库，没有 brickkit up 的对象"

module-check:
	@echo "N/A：非组件仓库，没有 module.New 契约"

all: check-version test image migrate-idempotent dag-check contract-check import-scan smoke module-check
