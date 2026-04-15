FROM golang:1.22 AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY main.go .
RUN CGO_ENABLED=0 GOOS=linux go build -o cpu-burner .

FROM gcr.io/distroless/static:nonroot
COPY --from=builder /app/cpu-burner /cpu-burner
USER 65534:65534
ENTRYPOINT ["/cpu-burner"]
