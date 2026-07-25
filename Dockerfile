# ===== 阶段 1：前端构建 =====
FROM node:22-alpine AS web-builder
WORKDIR /app/web
COPY web/package.json web/package-lock.json* ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

# ===== 阶段 2：Go 构建 =====
FROM golang:1.26-alpine AS go-builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
COPY web/embed.go web/
COPY --from=web-builder /app/web/dist web/dist
# 静态编译，去符号表压体积
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /go-stock ./cmd/server

# ===== 阶段 3：运行镜像（含 Python 历史行情降级源） =====
# 使用 Debian slim：AKShare 依赖 py_mini_racer 的预编译库需要 glibc/libstdc++，
# Alpine(musl) 会缺少 libstdc++.so.6 导致 AKShare 无法加载。
FROM python:3.12-slim-bookworm
RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates tzdata wget libstdc++6 && \
    rm -rf /var/lib/apt/lists/* && \
    useradd -m -u 1000 gostock
WORKDIR /opt/go-stock
COPY python-provider/requirements.txt python-provider/requirements.txt
RUN pip install --no-cache-dir -r python-provider/requirements.txt
COPY python-provider/fetch_kline.py python-provider/fetch_kline.py
COPY config/ai_prompt.md config/ai_prompt.md
ENV TZ=Asia/Shanghai
USER gostock
COPY --from=go-builder /go-stock /usr/local/bin/go-stock
EXPOSE 8480
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s \
  CMD wget -q -O /dev/null http://127.0.0.1:8480/api/v1/health || exit 1
ENTRYPOINT ["go-stock"]
