# ---- build stage ----
FROM golang:1.24-alpine AS build

WORKDIR /work

RUN apk add --no-cache git ca-certificates

# Copy Go module files first (cache-friendly)
COPY src/go.mod src/go.sum ./
RUN go mod download

# Copy only Go source code (exclude helm, install, .git, etc.)
COPY src/cmd ./cmd
COPY src/internal ./internal
COPY src/pkg ./pkg

# Build the binary
ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" \
    -o /out/mcp-kubernetes-server ./cmd/mcp-kubernetes-server

# ---- runtime stage ----
FROM alpine:3.20

ARG KUBECTL_VERSION=v1.30.4
ARG HELM_VERSION=v3.15.4

RUN apk add --no-cache ca-certificates curl bash tzdata \
    && update-ca-certificates

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

# Non-root user
RUN addgroup -S mcp && adduser -S -G mcp mcp

# Copy binary from build stage
COPY --from=build /out/mcp-kubernetes-server /usr/local/bin/mcp-kubernetes-server

USER mcp:mcp
WORKDIR /home/mcp

ENTRYPOINT ["/usr/local/bin/mcp-kubernetes-server"]

