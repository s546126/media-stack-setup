# syntax=docker/dockerfile:1
# LibreTV (github.com/LibreSpark/LibreTV) built from a pinned upstream commit
# plus this repo's danmaku.patch (弹幕 from Misaka). Multi-arch:
#   docker buildx build --platform linux/amd64,linux/arm64 -t libretv-danmaku .
# Bumping upstream: change LIBRETV_COMMIT, rebuild; if `git apply` fails the
# patch needs a rebase (see README.md).

FROM alpine:3.22 AS src
ARG LIBRETV_COMMIT=29cbeb3d37b4876f909a0354087a3531af0447f6
RUN apk add --no-cache git
RUN git clone https://github.com/LibreSpark/LibreTV.git /src \
 && git -C /src checkout --detach "$LIBRETV_COMMIT"
COPY danmaku.patch /tmp/danmaku.patch
RUN git -C /src apply --whitespace=nowarn /tmp/danmaku.patch && rm -rf /src/.git

# ---- below mirrors upstream's own Dockerfile ----
FROM node:22-alpine AS builder
# upstream's lockfile resolves part of its tarballs from npmmirror.com (China);
# rewrite to the registry you want (same tarballs, same integrity hashes)
ARG NPM_REGISTRY=https://registry.npmjs.org/
WORKDIR /app
COPY --from=src /src/package.json /src/package-lock.json ./
RUN sed -i "s#https://registry.npmmirror.com/#${NPM_REGISTRY}#g" package-lock.json \
 && npm ci --no-audit --no-fund
COPY --from=src /src ./
ENV DOCKER_BUILD=1
ENV NEXT_TELEMETRY_DISABLED=1
RUN npm run build

FROM node:22-alpine AS runner
WORKDIR /app
ENV NODE_ENV=production
ENV NEXT_TELEMETRY_DISABLED=1
ENV PORT=8080
ENV HOSTNAME=0.0.0.0
RUN addgroup -S nodejs && adduser -S nextjs -G nodejs
COPY --from=builder /app/.next/standalone ./
COPY --from=builder /app/.next/static ./.next/static
COPY --from=builder /app/public ./public
USER nextjs
EXPOSE 8080
CMD ["node", "server.js"]
