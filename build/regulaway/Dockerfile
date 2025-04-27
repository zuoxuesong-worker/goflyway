FROM golang:1.23.6 AS builder

ARG GO_LDFLAGS

WORKDIR /go/src/goflyway

ADD . /go/src/goflyway/

RUN go env -w GOPROXY=https://goproxy.cn

RUN go mod tidy

RUN CGO_ENABLED=0 go build -v -o /usr/local/bin/regulaway -ldflags "$GO_LDFLAGS -w -s" cmd/regulaway/main.go

FROM alpine:3.16

# 安装必要的依赖（如果有）
RUN apk --no-cache add ca-certificates
RUN apk add libc6-compat

# 设置工作目录
WORKDIR /root/

# 从构建阶段复制二进制文件到当前镜像
COPY --from=builder /usr/local/bin/regulaway .

