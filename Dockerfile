# Stage 1: Build the filwizard binary
FROM golang:1.23-bookworm AS builder
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=1 go build -o filwizard .

# Stage 2: Runtime image with all tools needed for contract deployment
FROM ubuntu:24.04

RUN apt-get update && apt-get install -y \
    curl jq git ca-certificates nodejs npm \
    && rm -rf /var/lib/apt/lists/*

# Install pnpm
RUN npm install -g pnpm@latest

# Install Foundry
RUN curl -L https://foundry.paradigm.xyz | bash
ENV PATH="/root/.foundry/bin:${PATH}"
RUN foundryup

# Copy filwizard binary
COPY --from=builder /build/filwizard /usr/local/bin/filwizard

# Pre-clone and compile FOC contracts (air-gapped operation)
WORKDIR /opt/filwizard
COPY config/filecoin-synapse.json /opt/filwizard/config/filecoin-synapse.json

RUN filwizard contract clone-config \
    --config /opt/filwizard/config/filecoin-synapse.json \
    --workspace /opt/filwizard/workspace

# Pre-clone and build synapse-sdk (needed for SP registration post-deploy-setup.js)
RUN git clone https://github.com/FilOzone/synapse-sdk /opt/synapse-sdk
WORKDIR /opt/synapse-sdk
RUN pnpm install && \
    pnpm --filter @filoz/synapse-core run build && \
    pnpm --filter @filoz/synapse-sdk run build && \
    pnpm add -w ethers

WORKDIR /opt/filwizard

# Default entrypoint — will be overridden by compose
ENTRYPOINT ["filwizard"]
