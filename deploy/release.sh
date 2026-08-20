#!/usr/bin/env bash
##############################################################################
# go-stock 标准发布脚本（服务器端）
#
# 标准流程：本机提交并推送 GitHub → 通过 Tabby MCP 在服务器执行本脚本 →
#   1. git 拉取 GitHub 最新代码（/opt/go-stock/src）
#   2. docker 构建镜像 go-stock:<短commit>
#   3. 备份并切换 docker-compose.yml 指向新镜像
#   4. 滚动更新容器 + 健康验证（health / 迁移日志 / 风险感知端点）
#   5. 任一验证失败自动回滚到上一版本
#
# 用法（服务器上执行）：
#   deploy/release.sh                  # 发布 origin/main 最新提交
#   deploy/release.sh v1.5.5           # 发布指定 tag / 分支 / commit
#   deploy/release.sh main --skip-pull # 不拉远端，用本地已 checkout 代码重建
#
# 约定：
#   - 应用目录 /opt/go-stock：docker-compose.yml 与 .env 常驻，不受发布影响
#   - 源码目录 /opt/go-stock/src：脚本管理的 git 克隆，勿手工改动
#   - 发布日志 /opt/go-stock/releases/logs/release-<时间>-<commit>.log
#   - 并发保护：/tmp/go-stock-release.lock，异常残留时 rmdir 解锁
##############################################################################
set -euo pipefail

APP_DIR="/opt/go-stock"
SRC_DIR="${APP_DIR}/src"
REPO_URL="https://github.com/brlanweb/go-stock.git"
COMPOSE_FILE="${APP_DIR}/docker-compose.yml"
HEALTH_URL="http://127.0.0.1:8480/api/v1/health"
RISK_URL="http://127.0.0.1:8480/api/v1/risk/gate"
LOCK_DIR="/tmp/go-stock-release.lock"
LOG_DIR="${APP_DIR}/releases/logs"
HEALTH_RETRIES=30   # 健康检查重试次数（×2s = 最多 60s）
MIN_DISK_GB=5       # 构建所需最低磁盘余量

REF="${1:-origin/main}"
SKIP_PULL="${2:-}"

log()  { printf '[%s] %s\n' "$(date '+%F %T')" "$*"; }
fail() { log "ERROR: $*"; exit 1; }

# ---------- 0. 前置检查 ----------
[ -f "${COMPOSE_FILE}" ] || fail "未找到 ${COMPOSE_FILE}"
[ -f "${APP_DIR}/.env" ] || fail "未找到 ${APP_DIR}/.env（含数据库/AI 配置，发布不生成该文件）"
command -v git >/dev/null || fail "git 未安装"
command -v docker >/dev/null || fail "docker 未安装"

free_gb=$(df -BG --output=avail / | tail -1 | tr -dc '0-9')
[ "${free_gb}" -ge "${MIN_DISK_GB}" ] || fail "磁盘余量不足 ${MIN_DISK_GB}G（当前 ${free_gb}G），请先清理"

# 并发锁：mkdir 原子性保证同一时刻只有一次发布
if ! mkdir "${LOCK_DIR}" 2>/dev/null; then
  fail "另一次发布正在进行（存在 ${LOCK_DIR}）；确认无发布后可执行 rmdir ${LOCK_DIR} 解锁"
fi
trap 'rmdir "${LOCK_DIR}" 2>/dev/null || true' EXIT

mkdir -p "${LOG_DIR}"

# ---------- 1. 更新代码 ----------
if [ ! -d "${SRC_DIR}/.git" ]; then
  log "首次发布：克隆仓库到 ${SRC_DIR}"
  git clone "${REPO_URL}" "${SRC_DIR}"
fi
cd "${SRC_DIR}"
if [ "${SKIP_PULL}" != "--skip-pull" ]; then
  log "拉取远端（目标: ${REF}）"
  git fetch origin --tags --prune
fi
git checkout --quiet --detach "${REF}" 2>/dev/null \
  || git checkout --quiet --detach "origin/${REF}" \
  || fail "无法 checkout: ${REF}"
COMMIT="$(git rev-parse --short HEAD)"
TAG="${COMMIT}"
IMAGE="go-stock:${TAG}"
STAMP="$(date +%Y%m%d-%H%M%S)"
BUILD_LOG="${LOG_DIR}/release-${STAMP}-${COMMIT}.log"
log "目标提交: $(git log --oneline -1)"

# 记录当前镜像，用于回滚与幂等判断
PREV_IMAGE="$(grep -E '^\s*image:\s*go-stock:' "${COMPOSE_FILE}" | awk '{print $2}' | head -1)"
log "当前运行镜像: ${PREV_IMAGE:-<未知>}"
if [ "${PREV_IMAGE}" = "${IMAGE}" ] && docker inspect go-stock --format '{{.State.Status}}' 2>/dev/null | grep -q running; then
  log "目标提交与当前运行版本一致（${IMAGE}），无需发布"
  exit 0
fi

# ---------- 2. 构建镜像 ----------
log "构建镜像 ${IMAGE}（日志: ${BUILD_LOG}）…"
if ! docker build -t "${IMAGE}" "${SRC_DIR}" >"${BUILD_LOG}" 2>&1; then
  tail -20 "${BUILD_LOG}" || true
  fail "镜像构建失败，compose 未改动，线上不受影响；完整日志见 ${BUILD_LOG}"
fi
log "构建完成: $(docker images "${IMAGE}" --format '{{.Repository}}:{{.Tag}} {{.Size}}')"

# ---------- 3. 切换 compose ----------
COMPOSE_BAK="${COMPOSE_FILE}.bak-${STAMP}-pre-${COMMIT}"
cp "${COMPOSE_FILE}" "${COMPOSE_BAK}"
log "compose 已备份: ${COMPOSE_BAK}"
# 只替换 go-stock 服务的 image 与 build 上下文；redis 等其他服务不受影响
sed -i -E "s|^(\s*image:\s*)go-stock:.*|\1${IMAGE}|" "${COMPOSE_FILE}"
sed -i -E "s|^(\s*build:\s*).*|\1${SRC_DIR}|" "${COMPOSE_FILE}"

rollback() {
  log "验证失败，回滚到 ${PREV_IMAGE:-上一版本}…"
  cp "${COMPOSE_BAK}" "${COMPOSE_FILE}"
  docker compose -f "${COMPOSE_FILE}" up -d --force-recreate go-stock >>"${BUILD_LOG}" 2>&1 || true
  for _ in $(seq 1 "${HEALTH_RETRIES}"); do
    curl -sf -m 3 "${HEALTH_URL}" >/dev/null 2>&1 && { log "回滚完成，旧版本已恢复"; return; }
    sleep 2
  done
  log "严重：回滚后健康检查仍失败，需要人工介入！"
}

# ---------- 4. 滚动更新 + 验证 ----------
log "滚动更新容器…"
if ! docker compose -f "${COMPOSE_FILE}" up -d go-stock >>"${BUILD_LOG}" 2>&1; then
  rollback; fail "docker compose up 失败，已回滚"
fi

DEPLOY_AT="$(date '+%Y-%m-%dT%H:%M:%S')"
log "健康检查（最多 $((HEALTH_RETRIES * 2))s）…"
healthy=0
for _ in $(seq 1 "${HEALTH_RETRIES}"); do
  if curl -sf -m 3 "${HEALTH_URL}" >/dev/null 2>&1; then healthy=1; break; fi
  sleep 2
done
if [ "${healthy}" -ne 1 ]; then
  docker logs go-stock --since "${DEPLOY_AT}" 2>&1 | tail -20 || true
  rollback; fail "健康检查超时，已回滚"
fi

# 迁移/启动错误检查：只看本次启动之后的日志
if docker logs go-stock --since "${DEPLOY_AT}" 2>&1 | grep -q 'level=ERROR'; then
  docker logs go-stock --since "${DEPLOY_AT}" 2>&1 | grep 'level=ERROR' | tail -5
  rollback; fail "启动日志出现 ERROR（多为迁移失败），已回滚"
fi

# 冒烟：风险感知端点（存在性验证，非致命——旧版本无此端点时仅告警）
if final_level=$(curl -sf -m 10 "${RISK_URL}" | grep -o '"final_level":"[a-z]*"' | cut -d'"' -f4); then
  log "风险感知冒烟通过: final_level=${final_level}"
else
  log "WARN: /risk/gate 冒烟未通过（请人工确认）"
fi

# ---------- 5. 收尾 ----------
docker image prune -f >/dev/null 2>&1 || true
# 只保留最近 5 份 compose 备份与发布日志，避免目录膨胀
ls -t "${COMPOSE_FILE}".bak-* 2>/dev/null | tail -n +6 | xargs -r rm -f
ls -t "${LOG_DIR}"/release-*.log 2>/dev/null | tail -n +6 | xargs -r rm -f

log "发布成功 ✅  ${PREV_IMAGE:-<初始>} → ${IMAGE}"
log "  提交:   $(git log --oneline -1)"
log "  容器:   $(docker inspect go-stock --format '{{.Config.Image}} {{.State.Status}}')"
log "  回滚:   cp ${COMPOSE_BAK} ${COMPOSE_FILE} && docker compose -f ${COMPOSE_FILE} up -d --force-recreate go-stock"
