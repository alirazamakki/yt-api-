# YouTube to MP3 API - High Traffic Optimized

This is a high-traffic optimized version of the YouTube to MP3 conversion API designed to handle thousands of concurrent requests.

## 🚀 Key Optimizations for High Traffic

### 1. **Scalability Improvements**
- **Worker Pool**: Increased from 5 to 20 concurrent workers
- **Queue Capacity**: Increased from 100 to 1000 pending jobs
- **Rate Limiting**: 100 requests/second with 200 burst capacity
- **Horizontal Scaling**: Ready for multiple API instances behind load balancer

### 2. **Caching & Storage**
- **Redis Integration**: Job status and metadata caching
- **Deduplication**: Prevents duplicate conversions for same URL
- **Job Persistence**: Jobs survive server restarts
- **Memory Optimization**: Efficient memory usage with atomic counters

### 3. **Performance Features**
- **Connection Pooling**: Optimized database connections
- **Request Deduplication**: Avoids processing same URL multiple times
- **Graceful Shutdown**: Proper cleanup on server restart
- **Health Monitoring**: Real-time health checks and metrics

### 4. **Load Balancing & Security**
- **Nginx Load Balancer**: Distributes traffic across multiple API instances
- **Rate Limiting**: Per-endpoint rate limiting
- **Security Headers**: CORS, XSS protection, etc.
- **Connection Limits**: Prevents resource exhaustion

## 📊 Monitoring & Metrics

### API Endpoints
- `POST /extract` - Start conversion. Body: `{ url, idempotency_key?, callback_url? }`
- `GET /status/{job_id}` - Check job status.
- `GET /download/{job_id}.mp3` - Download MP3 (Range supported).
- `DELETE /delete/{job_id}` - Delete job/file (API key required).
- `GET /health` - Server health status.
- `GET /metrics` - JSON metrics (human/debug).
- `GET /metrics/prom` - Prometheus exposition format (for Prometheus scrapes).
- `GET /stats` - Application statistics.

### Key Metrics
- Active jobs count
- Queue length
- Success/failure rates
- Processing times
- Memory usage
- Uptime
- Queue fill ratio (`ytmp3_queue_fill_ratio`)
- Queue over 80% flag (`ytmp3_queue_over_80`)

## 🐳 Deployment Options

### Option 1: Docker Compose (Recommended)
```bash
# Start all services
docker-compose up -d

# Scale API instances
docker-compose up -d --scale api=3

# View logs
docker-compose logs -f api
```

### Option 2: Kubernetes
```bash
# Apply Kubernetes manifests
kubectl apply -f k8s/

# Scale deployment
kubectl scale deployment ytmp3-api --replicas=5
```

## ⚙️ Configuration

### Environment Variables
```bash
# Redis Configuration
REDIS_ADDR=redis:6379
REDIS_PASSWORD=your_password
REDIS_DB=0

# Worker Configuration
WORKER_POOL_SIZE=20
JOB_QUEUE_CAPACITY=1000
MAX_JOB_RETRIES=3

# Rate Limiting
REQUESTS_PER_SECOND=100
BURST_SIZE=200

# Job Expiration
JOB_EXPIRATION_HOURS=24

# External Tool Timeouts (long-video reliability)
YTDLP_TIMEOUT=120s
FFMPEG_MIN_TIMEOUT=20m
FFMPEG_MAX_TIMEOUT=90m

# CORS and Auth
ALLOWED_ORIGINS="*"               # or comma-separated; server echoes matching Origin
REQUIRE_API_KEY=false              # true to enforce X-API-Key on protected routes
API_KEYS="key1,key2"             # comma-separated keys
PER_IP_RPS=10
PER_IP_BURST=20
ADMIN_USER=""                    # set to enable /admin
ADMIN_PASS=""
```

### Nginx Configuration
- Rate limiting per endpoint
- Connection limits
- Caching for downloads
- Security headers
- Load balancing
- Proxy cache zone defined and used for `/download/`
- `stub_status` exposed at `/nginx_status` (restricted) for exporters
- HSTS should be enabled only on HTTPS vhosts, not on plain HTTP

## 📈 Performance Expectations

### Capacity
- **Concurrent Jobs**: 20 active + 1000 queued
- **Request Rate**: 100 requests/second sustained
- **Burst Capacity**: 200 requests/second
- **Memory Usage**: ~2GB per instance
- **CPU Usage**: ~2 cores per instance

### Response Times
- **Job Creation**: < 100ms
- **Status Check**: < 50ms
- **File Download**: Depends on file size
- **Health Check**: < 10ms

## 🔧 Production Setup

### 1. Infrastructure Requirements
- **CPU**: 2+ cores per API instance
- **RAM**: 2GB+ per API instance
- **Storage**: SSD recommended for downloads
- **Network**: High bandwidth for downloads

### 2. Redis Setup
```bash
# Redis configuration for high traffic
redis-server --maxmemory 2gb --maxmemory-policy allkeys-lru
```

### 3. Monitoring Setup
- Prometheus for metrics collection
- Grafana for visualization
- AlertManager for notifications
- Log aggregation (ELK stack)

### 4. Security Considerations
- Use HTTPS in production
- Implement authentication
- Rate limiting per user/IP
- Input validation
- File size limits

## 🚨 Troubleshooting

### Common Issues

1. **High Memory Usage**
   - Check Redis memory usage
   - Monitor job queue length
   - Adjust worker pool size

2. **Slow Response Times**
   - Check Redis connectivity
   - Monitor worker utilization
   - Scale horizontally

3. **Failed Conversions**
   - Check yt-dlp/ffmpeg availability
   - Monitor error logs
   - Check network connectivity

### Monitoring Commands
```bash
# Check API health
curl http://localhost:8080/health

# View JSON metrics (human)
curl http://localhost:8080/metrics

# View Prometheus metrics
curl http://localhost:8080/metrics/prom

# Check Redis
redis-cli ping

# View logs
docker-compose logs -f api
```

## 📝 API Usage Examples

### Start Conversion
```bash
curl -X POST http://localhost:8080/extract \
  -H "Content-Type: application/json" \
  -d '{"url": "https://www.youtube.com/watch?v=VIDEO_ID"}'
```

### Check Status
```bash
curl http://localhost:8080/status/JOB_ID
```

### Download File
```bash
curl -O http://localhost:8080/download/JOB_ID.mp3
```

## 🔄 Scaling Strategy

### Vertical Scaling
- Increase worker pool size
- Add more memory/CPU
- Optimize Redis configuration

### Horizontal Scaling
- Deploy multiple API instances
- Use load balancer
- Implement session affinity if needed

### Database Scaling
- Redis Cluster for high availability
- Read replicas for status checks
- Sharding for large datasets

This optimized version can handle significantly more traffic than the original implementation while maintaining reliability and performance.