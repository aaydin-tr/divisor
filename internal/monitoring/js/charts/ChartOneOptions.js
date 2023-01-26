const ctxChartOne = document.getElementById("chartOne");
const ctxChartTwo = document.getElementById("chartTwo");

const darkOption = {
  scales: {
    y: {
      min: 0,
      max: 100,
      beginAtZero: true,
    },
  },
  // elements: {
  //   point: {
  //     radius: 0,
  //     hitRadius: 20,
  //   },
  //   line: {
  //     lineTension: 0.2,
  //     backgroundColor: "black",
  //   },
  // },
  animation: true,
};

const lightOption = {
  scales: {
    y: {
      min: 0,
      max: 100,
      beginAtZero: true,
    },
  },

  elements: {
    point: {
      radius: 0,
      hitRadius: 20,
    },
    line: {
      lineTension: 0.2,
      backgroundColor: "red",
    },
  },
  animation: true,
};

const chartOne = new Chart(ctxChartOne, {
  type: "line",
  data: {
    labels: [],
    datasets: [
      {
        label: "",
        data: [],
        fill: true,
      },
    ],
  },
  options: darkOption,
});
console.log(chartOne.options);
setInterval(() => {
  const timestamp = dateFormatter(new Date());
  if (chartOne.data.labels.length > 10) {
    chartOne.data.labels.shift();
    chartOne.data.datasets[0].data.shift();
  }
  chartOne.data.datasets[0].data.push(getRandomInt(100));
  chartOne.data.labels.push(timestamp);

  chartOne.update();
}, 1000);

const chartTwo = new Chart(ctxChartTwo, {
  type: "line",
  data: {
    labels: [],
    datasets: [
      {
        label: "",
        data: [],
        fill: true,
      },
      {
        label: "",
        data: [],
        fill: true,
      },
    ],
  },
  options: darkOption,
});

setInterval(() => {
  const timestamp = dateFormatter(new Date());
  if (chartTwo.data.labels.length > 10) {
    chartTwo.data.labels.shift();
    chartTwo.data.datasets[0].data.shift();
    chartTwo.data.datasets[1].data.shift();
  }
  chartTwo.data.datasets[0].data.push(getRandomInt(100));
  chartTwo.data.datasets[1].data.push(getRandomInt(100));
  chartTwo.data.labels.push(timestamp);

  chartTwo.update();
}, 500);
