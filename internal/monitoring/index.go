package monitoring

var index = `
<!DOCTYPE html>
<html lang="en">

<head>
  <meta charset="UTF-8" />
  <meta http-equiv="X-UA-Compatible" content="IE=edge" />
  <meta name="viewport" content="width=device-width, initial-scale=1.0" />
  <link rel="preconnect" href="https://fonts.googleapis.com" />
  <link rel="preconnect" href="https://fonts.gstatic.com" crossorigin />
  <link href="https://fonts.googleapis.com/css2?family=PT+Serif&display=swap" rel="stylesheet" />
  <title>Monitor</title>
</head>
<style>
  * {
    font-family: "PT Serif", serif;
  }

  body {
    margin: 20px 0;
    padding: 0;
    background-color: #f7f7f7;
    height: 100%;
    box-sizing: border-box;
  }

  .box-container {
    background-color: white;
    width: 24%;
    border-radius: 10px;
    border: 1px solid #e4e4e4;
    margin-right: 10px;
    margin-top: 10px;
    padding: 16px 2px;
  }

  .box-container>.title {
    width: 100%;
    text-align: center;
    font-size: 20px;
    margin-bottom: 20px;
  }

  .backend-detail {
    display: grid;
    justify-content: center;
    font-size: 14px;
    grid-template-columns: auto auto auto;
    column-gap: 12%;
    grid-template-rows: 30px;
  }
</style>

<body>
  <div style="
        width: 100%;
        display: flex;
        justify-content: center;
        align-items: center;
        flex-wrap: wrap;
      ">
    <div id="box-container" class="box-container">
      <div class="title">Cpu</div>
      <canvas id="cpu"></canvas>
    </div>

    <div id="box-container" class="box-container">
      <div class="title">Memory</div>
      <canvas id="ram"></canvas>
    </div>

    <div id="box-container" class="box-container">
      <div class="title">Goroutine Count</div>
      <canvas id="total_goroutine"></canvas>
    </div>
    <div id="box-container" class="box-container">
      <div class="title">Open Conn Count</div>
      <canvas id="open_conn_count"></canvas>

    </div>
  </div>
  <div>
    <h2 align="center" style="margin-top: 56px;">
      Servers
    </h2>
  </div>
  <div id="backends" style="
      width: 100%;
      display: flex;
      justify-content: center;
      align-items: center;
      flex-wrap: wrap;
    ">
  </div>
</body>
<script src="https://cdnjs.cloudflare.com/ajax/libs/Chart.js/4.1.2/chart.umd.js"></script>
<script>
  const dateFormatter = (date) => {
    const hours = date.getHours();
    const minute = date.getMinutes();
    const second = date.getSeconds();

    return hours + ":" + minute + ":" + second;
  };

  const cpu = document.getElementById("cpu");
  const ram = document.getElementById("ram");

  const totalGoroutine = document.getElementById("total_goroutine");
  const openConnCount = document.getElementById("open_conn_count");

  const backendsContainer = document.getElementById("backends")

  const generalOption = {
    animations: false,
    elements: {
      point: {
        radius: 0.2,
        hitRadius: 20,
      },
    },
  };

  const cpuChart = new Chart(cpu, {
    type: "line",
    data: {
      labels: [],
      datasets: [
        {
          label: "Process Percent",
          data: [],
          fill: true,
        },
        {
          label: "Total Percent",
          data: [],
          fill: true,
        },
      ],
    },
    options: {
      ...generalOption,
      scales: {
        y: {
          min: 0,
          max: 100,
          beginAtZero: true,
          ticks: {
            callback: function (value) {
              return value + "%";
            },
          },
        },
      },
    },
  });

  const ramChart = new Chart(ram, {
    type: "line",
    data: {
      labels: [],
      datasets: [
        {
          label: "Process Percent",
          data: [],
          fill: true,
        },
        {
          label: "Total Percent",
          data: [],
          fill: true,
        },
      ],
    },
    options: {
      ...generalOption,
      scales: {
        y: {
          min: 0,
          max: 100,
          beginAtZero: true,
          ticks: {
            callback: function (value) {
              return value + "%";
            },
          },
        },
      },
    },
  });

  const totalGoroutineChart = new Chart(totalGoroutine, {
    type: "line",
    data: {
      labels: [],
      datasets: [
        {
          label: "Goroutine Count",
          data: [],
          fill: true,
        },
      ],
    },
    options: {
      ...generalOption,
      scales: {
        y: {
          beginAtZero: true,
        },
      },
    },
  });

  const openConnCountChart = new Chart(openConnCount, {
    type: "line",
    data: {
      labels: [],
      datasets: [
        {
          label: "Connection Count",
          data: [],
          fill: true,
        },
      ],
    },
    options: {
      ...generalOption,
      scales: {
        y: {
          beginAtZero: true,
        },
      },
    },
  });

  const charts = [cpuChart, ramChart, totalGoroutineChart, openConnCountChart];

  const fetchDatas = () => {
    fetch("/stats")
      .then((res) => res.json())
      .then(({ cpu, memory, backends, total_goroutine, open_conn_count }) => {
        const timestamp = dateFormatter(new Date());

        charts.forEach((chart) => {
          if (chart.data.labels.length > 50) {
            chart.data.datasets.forEach(function (dataset) {
              dataset.data.shift();
            });
            chart.data.labels.shift();
          }
          chart.data.labels.push(timestamp);
        });

        cpuChart.data.datasets[0].label = "Process Percent (" + cpu.process_percent.toFixed(2) + "%)";
        cpuChart.data.datasets[1].label = "Total Percent (" + cpu.total_percent.toFixed(2) + "%)";

        cpuChart.data.datasets[0].data.push(cpu.process_percent.toFixed(2));
        cpuChart.data.datasets[1].data.push(cpu.total_percent.toFixed(2));

        ramChart.data.datasets[0].data.push(memory.process_percent.toFixed(2));
        ramChart.data.datasets[1].data.push(memory.total_percent.toFixed(2));

        ramChart.data.datasets[0].label = "Process Percent (" + memory.process_percent.toFixed(2) + "%)";
        ramChart.data.datasets[1].label = "Total Percent (" + memory.total_percent.toFixed(2) + "%)";

        totalGoroutineChart.data.datasets[0].data.push(total_goroutine);
        openConnCountChart.data.datasets[0].data.push(open_conn_count);

        backends.forEach(({ backend_hash, addr, total_req_count, avg_res_time, last_use_time, conns_count, is_host_alive }) => {
          const { backendChart, detailDiv } = createOrGetBackend(backend_hash, addr, total_req_count, avg_res_time.toFixed(2), last_use_time)
          backendChart.data.datasets[0].data.push(conns_count);
          updateDetail(backend_hash, total_req_count, avg_res_time.toFixed(2), last_use_time)
        })

        charts.forEach((chart) => {
          chart.update();
        });
      });
  };

  fetchDatas()
  setInterval(() => {
    fetchDatas();
  }, 3000);


  const createOrGetBackend = (backend_hash, addr, total_req_count, avg_res_time, last_use_time) => {
    const backendChart = document.getElementById(backend_hash + "-chart")
    if (backendChart) {
      return {
        backendChart: charts[backendChart.getAttribute("data")],
        detailDiv: document.getElementById(backend_hash + "-detail")
      }
    }

    const backendContainer = backendsContainer.appendChild(document.createElement("div"))
    backendContainer.classList.add("box-container")
    backendContainer.setAttribute("id", backend_hash)
    backendContainer.style.paddingBottom = "15px"
    const backendTitle = backendContainer.appendChild(document.createElement("div"))
    backendTitle.classList.add("title")
    backendTitle.innerText = addr
    const backendCanvas = backendContainer.appendChild(document.createElement("canvas"))
    backendCanvas.setAttribute("id", backend_hash + "-chart")
    const i = createBackendChart(backendCanvas)
    backendCanvas.setAttribute("data", i)

    const detailDiv = backendContainer.appendChild(document.createElement("div"))
    detailDiv.setAttribute("id", backend_hash + "-detail")
    detailDiv.classList.add("backend-detail")

    const totalReqCountTitle = detailDiv.appendChild(document.createElement("h4"))
    totalReqCountTitle.innerText = "Total Req Count"
    const avgResTimeTitle = detailDiv.appendChild(document.createElement("h4"))
    avgResTimeTitle.innerText = "Avg Res Time"
    const lastUseTimeTitle = detailDiv.appendChild(document.createElement("h4"))
    lastUseTimeTitle.innerText = "Last Use Time"

    const totalReqCount = detailDiv.appendChild(document.createElement("p"))
    totalReqCount.setAttribute("id", backend_hash + "-total_req_count")
    totalReqCount.innerText = total_req_count

    const avgResTime = detailDiv.appendChild(document.createElement("p"))
    avgResTime.setAttribute("id", backend_hash + "-avg_res_time")
    avgResTime.innerText = avg_res_time

    const lastUseTime = detailDiv.appendChild(document.createElement("p"))
    lastUseTime.setAttribute("id", backend_hash + "-last_use_time")
    lastUseTime.innerText = new Date(last_use_time).toLocaleString()

    return { backendChart: charts[i], detailDiv: detailDiv }
  }

  const createBackendChart = (backendCanvas) => {
    const backendChart = new Chart(backendCanvas, {
      type: "line",
      data: {
        labels: [],
        datasets: [
          {
            label: "Connection Count",
            data: [],
            fill: true,
          },
        ],
      },
      options: {
        ...generalOption,
        scales: {
          y: {
            beginAtZero: true,
          },
        },
      },
    })
    charts.push(backendChart)
    return charts.length - 1
  }


  const updateDetail = (backend_hash, total_req_count, avg_res_time, last_use_time) => {
    document.getElementById(backend_hash + "-total_req_count").innerText = total_req_count
    document.getElementById(backend_hash + "-avg_res_time").innerText = avg_res_time + " ms"
    document.getElementById(backend_hash + "-last_use_time").innerText = new Date(last_use_time).toLocaleString()
  }

</script>

</html>
`
