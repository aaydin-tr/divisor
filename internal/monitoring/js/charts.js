const dateFormatter = (date) => {
  const hours = date.getHours();
  const minute = date.getMinutes();
  const second = date.getSeconds();

  return hours + ":" + minute + ":" + second;
};

const getRandomInt = (max) => {
  return Math.floor(Math.random() * max);
};

const cpu = document.getElementById("cpu");
const ram = document.getElementById("ram");

const totalGoroutine = document.getElementById("total_goroutine");
const openConnCount = document.getElementById("open_conn_count");

const generalOption = {
  // animations: {
  //     tension: {
  //         easing: 'easeInOutBack',
  //         loop: false
  //     }
  // },
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
        label: "Goroutines",
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

setInterval(() => {
  fetchDatas();
}, 1000);

const fetchDatas = () => {
  fetch("http://localhost:8001/")
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
      cpuChart.data.datasets[0].label =
        "Process Percent (" + cpu.process_percent.toFixed(2) + "%)";
      cpuChart.data.datasets[1].label =
        "Total Percent (" + cpu.total_percent.toFixed(2) + "%)";
      cpuChart.data.datasets[0].data.push(cpu.process_percent.toFixed(2));
      cpuChart.data.datasets[1].data.push(cpu.total_percent.toFixed(2));

      ramChart.data.datasets[0].data.push(memory.process_percent);
      ramChart.data.datasets[1].data.push(memory.total_percent);

      totalGoroutineChart.data.datasets[0].data.push(total_goroutine);
      openConnCountChart.data.datasets[0].data.push(open_conn_count);

      charts.forEach((chart) => {
        chart.update();
      });
    });
};
