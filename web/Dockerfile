# syntax=docker/dockerfile:1

FROM node:26-alpine AS build
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci
COPY . .
# Also writes .svelte-kit/nginx/csp-hashes.conf (CSP hashes of the inline shell code, scripts/csp.mjs)
# and fails when the shell contains inline code the Content-Security-Policy would block.
RUN npm run build

FROM nginxinc/nginx-unprivileged:stable-alpine
# Static SPA served on 8080 as the unprivileged `nginx` user (uid 101).
COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY nginx-security-headers.conf /etc/nginx/snippets/security-headers.conf
COPY --from=build /app/.svelte-kit/nginx/csp-hashes.conf /etc/nginx/snippets/csp-hashes.conf
COPY --from=build /app/build /usr/share/nginx/html
# A broken config (or a missing generated include) fails the image build, not the rollout.
RUN nginx -t
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s \
  CMD wget -q -O /dev/null http://127.0.0.1:8080/healthz || exit 1
