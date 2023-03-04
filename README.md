<br />
<div align="center">
  <h3 align="center">Divisor</h3>

  <p align="center">
    A fast and easy to configure load balancer
    <br />
    <br />
  </p>
</div>


## About The Project
This project is designed to provide a fast and easy-to-configure load balancer in Go language. It currently includes **round-robin**, **weighted round-robin**, **ip-hash** and **random** algorithms, but we have more to add to our [TODO](#todo) list.

The project is developed using the [fasthttp](https://github.com/valyala/fasthttp) library, which ensures high performance. Its purpose is to distribute the load evenly among multiple servers by routing incoming requests.

The project aims to simplify the configuration process for users while performing the essential functions of load balancers. Therefore, it offers several configuration options that can be adjusted to meet the user's needs.

This project is particularly suitable for large-scale applications and websites. It can be used for any application that requires a load balancer, thanks to its high performance, ease of configuration, and support for different algorithms.


## Features
- Fast and easy-to-configure load balancer for Go language.
- Supports round-robin, weighted round-robin, IP hash, and random algorithms.
- Uses the fasthttp library for high performance and scalability.
- Offers multiple configuration options to suit user needs.
- Can handle large-scale applications and websites.
- Provides a simple and intuitive API for ease of use.
- Includes a built-in monitoring system that displays real-time information on the system's CPU usage, RAM usage, number of Goroutines, and open connections.
- Provides information on each server's average response time, total request count, and last time used.
- Lightweight and efficient implementation for minimal resource usage.

## Installation

#### Downloading the Release
The latest release of Divisor can be downloaded from the [releases](https://github.com/aaydin-tr/divisor/releases) page. Choose the appropriate binary for your system, download and extract the archive, and then move the binary to a directory in your system's $PATH variable (e.g. /usr/local/bin).

#### Building from Source
Alternatively, you can build Divisor from source by cloning this repository to your local machine and running the following commands:

```bash
git clone https://github.com/aaydin-tr/divisor.git &&
cd divisor &&
go build -o divisor &&
./divisor
```

#### Using go install
You can also install the load balancer using the go install command:

```bash
go install github.com/aaydin-tr/divisor@latest
```

This will install the divisor binary to your system's `$GOPATH/bin` directory. Make sure this directory is included in your system's `$PATH` variable to make the divisor accessible from anywhere.

That's it! You're now ready to use the load balancer in your project.

## Limitations
While Divisor has several features and benefits, it also has some limitations to be aware of:

- Divisor version of the project only includes four load balancing algorithms: round-robin, weighted round-robin, IP-hash, and random. Other algorithms may be added in future releases.
- Divisor currently operates at layer 7, meaning it is specifically designed for HTTP/HTTPS load balancing. It does not support other protocols, such as TCP or UDP.
- Divisor does not support HTTP/2 or HTTP/3, which may be important for some applications.

Please keep these limitations in mind when considering whether this load balancer is the right choice for your project.

## TODO
While Divisor has several features, there are also some areas for improvement that are planned for future releases:

- [ ] Add support for other protocols, such as TCP or UDP.
- [ ] Support HTTP/2 and HTTP/3 protocols.
- [ ] Add more load balancing algorithms, such as,
  - [ ] sticky round-robin
  - [ ] least connection
- [ ] Improve performance and scalability for high-traffic applications.
- [ ] Expand monitoring capabilities to provide more detailed metrics and analytics.

By addressing these issues and adding new features, we aim to make Divisor an even more versatile and powerful tool for managing traffic in modern web applications.

## Contributors
<a href = "https://github.com/aaydin-tr/divisor/graphs/contributors">
  <img src = "https://contrib.rocks/image?repo=aaydin-tr/divisor"/>
</a>

## License
This project is licensed under the MIT License. See the LICENSE file for more information.

The MIT License is a permissive open-source software license that allows users to modify and redistribute the code, as long as the original license and copyright notice are included. This means that you are free to use Divisor for any purpose, including commercial projects, without having to pay any licensing fees or royalties. However, it is provided "as is" and without warranty of any kind, so use it at your own risk.