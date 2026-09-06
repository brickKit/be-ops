# 十八条第 8 条对 be-ops 不适用（它不是 brickKit 组件，没有健康检查）——
# 但基底仍带 shell，图个万一要 docker exec 进去调试
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
RUN go build -o /out/be-ops ./cmd/be-ops

FROM alpine:3.20
RUN apk add --no-cache wget
COPY --from=build /out/be-ops /usr/local/bin/be-ops
ENTRYPOINT ["be-ops"]
