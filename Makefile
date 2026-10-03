# be-ops 不是 brickKit 组件，但仍按总纲 §I 的 9 个门禁目标写。
.DEFAULT_GOAL := help
.PHONY: help check-version test test-pg image migrate-idempotent dag-check contract-check \
        import-scan smoke module-check all

help:  ## 列出所有目标
	@awk 'BEGIN{FS=":.*##"; printf "\n用法: make <目标>\n\n"} \
	     /^[a-zA-Z0-9_-]+:.*##/ {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2} \
	     /^##@/ {printf "\n\033[1m%s\033[0m\n", substr($$0,5)}' $(MAKEFILE_LIST)
	@echo ""

##@ 9 个门禁里对本仓库没意义的（非组件仓库，没有对应的东西）
check-version:  ## N/A：没有 component.yaml
	@echo "N/A：非组件仓库，没有 component.yaml"

image:  ## N/A：不产出 brickKit 部署镜像（这是 CLI 工具，直接 go build 用）
	docker build -t brickkit/be-ops:dev .

migrate-idempotent:  ## N/A：没有迁移
	@echo "N/A：非组件仓库，没有迁移"

contract-check:  ## N/A：没有 contracts/
	@echo "N/A：非组件仓库，没有 contracts/"

smoke:  ## N/A：没有 brickkit up 的对象
	@echo "N/A：非组件仓库，没有 brickkit up 的对象"

module-check:  ## N/A：没有 module.New 契约
	@echo "N/A：非组件仓库，没有 module.New 契约"

##@ 对本仓库真正有意义的
test:  ## 跑全部单测（-race）
	go test ./... -race

test-pg:  ## db-script 的真库测试：先起一次性容器 docker run -d --name sdkb-ops-pg16 -e POSTGRES_PASSWORD=x postgres:16-alpine，测完 docker rm -f
	BEOPS_PG16_CONTAINER=$${BEOPS_PG16_CONTAINER:-sdkb-ops-pg16} go test -count=1 ./internal/dbscript/ -run RealPostgres -v

dag-check:  ## 包依赖图无环（Go 编译器本身就不允许循环 import，这条恒过）
	@go list ./... >/dev/null && echo "✓ 包依赖图无环（Go 编译器本身就不允许循环 import）"

import-scan:  ## 铁律六：be-ops 不许依赖任何组件仓库（白名单：be-sdk-go、be-protocol）
	@bad="$$(go list -deps ./... 2>/dev/null | grep '^github.com/brickKit/' | grep -vE '^github.com/brickKit/be-ops($$|/)' | grep -vE '^github.com/brickKit/be-(sdk-go|protocol)($$|/)')"; \
	if [ -n "$$bad" ]; then \
		echo "✗ be-ops 不许依赖任何组件仓库：$$bad"; exit 1; \
	fi; \
	echo "✓ 零组件依赖（be-sdk-go、be-protocol 例外：be-protocol 是生成器的输入，按 go.mod 钉的 tag 读）"

##@ 汇总
all: check-version test image migrate-idempotent dag-check contract-check import-scan smoke module-check  ## 跑完整 9 项（含上面几条 N/A 直接过）
