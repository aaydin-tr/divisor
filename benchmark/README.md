# Benchmark Results

This document contains the results of load testing Divisor using a variety of test scenarios.

## Test Environment

The load tests were conducted using the following environment:

- CPU: AMD Ryzen 5 5500 3.60 GHz
- RAM: 16.0 GB
- Operating System: Windows 10 Pro 21H2

With this [docker-compose.yml](https://github.com/aaydin-tr/divisor/blob/main/benchmark/docker-compose.yml) file, we launched 4 different backend services and used these services in the tests. Used [bombardier](https://github.com/codesenberg/bombardier) as a HTTP(S) benchmarking tool, used Divisor built-in monitoring system for **Server stats**

### Round-robin

##### Config 
```yaml
type: round-robin
port: 8000
backends:
  - url: localhost:8080
  - url: localhost:7070
  - url: localhost:6060
  - url: localhost:5050
```
##### Result
```bash
bombardier -c 512 -t 10s "http://localhost:8000"

Bombarding http://localhost:8000 for 10s using 512 connection(s)
[=================================================================================================================] 10s
Done!
Statistics        Avg      Stdev        Max
  Reqs/sec     43250.29    6099.38   62211.20
  Latency       11.85ms    18.09ms      1.02s
  HTTP codes:
    1xx - 0, 2xx - 432027, 3xx - 0, 4xx - 0, 5xx - 0
    others - 0
  Throughput:    16.20MB/s
```
##### Server stats
| addr            | total_req_count | avg_res_time(ms) |
| ---------------| ---------------| ------------ |
| localhost:8080 | 108006          | 9.814075143973483 |
| localhost:7070 | 108007           | 9.94852185506495  |
| localhost:6060 | 108007           | 9.873785958317516  |
| localhost:5050 | 108007           | 9.71393520790319  |

### Weighted Round-robin

##### Config 
```yaml
type: w-round-robin
port: 8000
backends:
  - url: localhost:8080
    weight: 5
  - url: localhost:7070
    weight: 2
  - url: localhost:6060
    weight: 2
  - url: localhost:5050
    weight: 1
```

##### Result
```bash
bombardier -c 512 -t 10s "http://localhost:8000"

Bombarding http://localhost:8000 for 10s using 512 connection(s)
[=================================================================================================================] 10s
Done!
Statistics        Avg      Stdev        Max
  Reqs/sec     42840.75    6391.24   63013.18
  Latency       11.95ms    17.94ms      1.38s
  HTTP codes:
    1xx - 0, 2xx - 428556, 3xx - 0, 4xx - 0, 5xx - 0
    others - 0
  Throughput:    16.07MB/s
```
##### Server stats
| addr            | total_req_count | avg_res_time(ms) |
| ---------------| ---------------| ------------ |
| localhost:8080 | 214277          | 10.210041208342473 |
| localhost:7070 | 85712           | 9.513603696098563  |
| localhost:6060 | 85711           | 9.337109589200919  |
| localhost:5050 | 42856           | 9.127100056001494  |


### Random

##### Config 
```yaml
type: random
port: 8000
backends:
  - url: localhost:8080
  - url: localhost:7070
  - url: localhost:6060
  - url: localhost:5050
```

##### Result
```bash
bombardier -c 512 -t 10s "http://localhost:8000"

Bombarding http://localhost:8000 for 10s using 512 connection(s)
[=================================================================================================================] 10s
Done!
Statistics        Avg      Stdev        Max
  Reqs/sec     40655.88    7320.53   57079.60
  Latency       12.60ms    16.43ms      0.97s
  HTTP codes:
    1xx - 0, 2xx - 406473, 3xx - 0, 4xx - 0, 5xx - 0
    others - 0
  Throughput:    15.24MB/s
```
##### Server stats
| addr            | total_req_count | avg_res_time(ms) |
| ---------------| ---------------| ------------ |
| localhost:8080 | 102333          | 10.418789637751264 |
| localhost:7070 | 101397           | 10.439500182451157  |
| localhost:6060 | 101472           | 10.328671948912016  |
| localhost:5050 | 101271           | 10.406829200857107  |


### Ip-hash

##### Config 
```yaml
type: ip-hash
port: 8000
backends:
  - url: localhost:8080
  - url: localhost:7070
  - url: localhost:6060
  - url: localhost:5050
```

##### Result
```bash
bombardier -c 512 -t 10s "http://localhost:8000"

Bombarding http://localhost:8000 for 10s using 512 connection(s)
[=================================================================================================================] 10s
Done!
Statistics        Avg      Stdev        Max
  Reqs/sec     45066.93    6842.94   62311.60
  Latency       11.36ms    17.13ms      1.37s
  HTTP codes:
    1xx - 0, 2xx - 450520, 3xx - 0, 4xx - 0, 5xx - 0
    others - 0
  Throughput:    16.90MB/s
```
##### Server stats
| addr            | total_req_count | avg_res_time(ms) |
| ---------------| ---------------| ------------ |
| localhost:8080 | 450520          | 9.225002219657284 |
| localhost:7070 | 0           | 0  |
| localhost:6060 | 0           | 0  |
| localhost:5050 | 0           | 0  |



### Least-connection

##### Config 
```yaml
type: least-connection
port: 8000
backends:
  - url: localhost:8080
  - url: localhost:7070
  - url: localhost:6060
  - url: localhost:5050
```

##### Result
```bash
bombardier -c 512 -t 10s --header="Least:true" "http://localhost:8000"

Bombarding http://localhost:8000/stats for 10s using 512 connection(s)
[=================================================================================================================] 10s
Done!
Statistics        Avg      Stdev        Max
  Reqs/sec     39916.85    5426.21   50521.53
  Latency       12.81ms    19.29ms      1.39s
  HTTP codes:
    1xx - 0, 2xx - 400038, 3xx - 0, 4xx - 0, 5xx - 0
    others - 0
  Throughput:    16.11MB/s
```

> :warning: `localhost:8080` wait 75 milliseconds, `localhost:5050` wait 25 milliseconds before return response.

##### Server stats
| addr            | total_req_count | avg_res_time(ms) |
| ---------------| ---------------| ------------ |
| localhost:8080 | 14489          | 81.94609703913314 |
| localhost:7070 | 174030           | 5.924340630925703  |
| localhost:6060 | 175242           | 5.872507732164664  |
| localhost:5050 | 36277           | 31.916696529481488  |


## Conclusion

Based on the results of the load tests, we conclude that our load balancer is performing well under the given test scenarios. However, we recommend conducting additional load tests to further validate its performance under different conditions.
