// Package registry 读写 registry/ 下的四张全局册子（ports.tsv / schemas.tsv /
// permissions.tsv / data-scopes.tsv）。be-ops 的多个子命令（registry / gen /
// permissions / data-scopes）共用同一套解析逻辑，不许各自再实现一遍。
//
// 现在只是占位——实现放 Task 8（产出 6：全局端口册校验）起补，那时
// infra/scripts/registry-check.sh 的判据会原样搬过来，脚本退休为薄壳。
package registry
