package plan

type Workload struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Connections int    `json:"connections"`
	// RequestsPerSecond is the constant rate the load generator holds; zero
	// means every connection sends its next request as soon as one is answered.
	RequestsPerSecond          int  `json:"requests_per_second,omitempty"`
	CorrectCoordinatedOmission bool `json:"correct_coordinated_omission,omitempty"`
}

var Throughput = Workload{
	ID:          "throughput",
	Name:        "Throughput",
	Connections: 64,
}

var FixedRateLatency = Workload{
	ID:                         "fixed-rate-latency",
	Name:                       "Fixed-rate latency",
	Connections:                64,
	RequestsPerSecond:          5000,
	CorrectCoordinatedOmission: true,
}
