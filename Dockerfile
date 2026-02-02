# ---- build stage ----
FROM golang:1.23-alpine AS build

WORKDIR /src

# Needed for fetching private/public modules + building
RUN apk add --no-cache git ca-certificates

# Copy go mod first for better caching
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the source
COPY . .

# Build a static-ish binary (CGO disabled)
ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /out/mcp-kubernetes-server ./cmd/mcp-kubernetes-server


# ---- runtime stage ----
FROM alpine:3.20

# Versions (pin them for reproducible builds)
ARG KUBECTL_VERSION=v1.30.4
ARG HELM_VERSION=v3.15.4

# Runtime deps:
# - ca-certificates: TLS to apiserver / registries / etc
# - curl: to download kubectl/helm
# - bash: some helm plugins/scripts expect it (optional but practical)
# - tzdata: nice-to-have (optional)
RUN apk add --no-cache ca-certificates curl bash tzdata && update-ca-certificates

# Install kubectl
RUN curl -fsSL -o /usr/local/bin/kubectl \
      "https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/amd64/kubectl" \
    && chmod +x /usr/local/bin/kubectl \
    && kubectl version --client=true --output=yaml || true

# Install helm
RUN curl -fsSL -o /tmp/helm.tar.gz \
      "https://get.helm.sh/helm-${HELM_VERSION}-linux-amd64.tar.gz" \
    && tar -xzf /tmp/helm.tar.gz -C /tmp \
    && mv /tmp/linux-amd64/helm /usr/local/bin/helm \
    && chmod +x /usr/local/bin/helm \
    && rm -rf /tmp/helm.tar.gz /tmp/linux-amd64 \
    && helm version || true

# Create non-root user
RUN addgroup -S mcp && adduser -S -G mcp mcp

# Copy binary
COPY --from=build /out/mcp-kubernetes-server /usr/local/bin/mcp-kubernetes-server

USER mcp:mcp
WORKDIR /home/mcp

# Default: stdio transport (like your current Run() default)
ENTRYPOINT ["/usr/local/bin/mcp-kubernetes-server"]
