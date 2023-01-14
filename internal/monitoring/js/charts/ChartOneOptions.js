const ctx = document.getElementById("lineChart");

const lineChart = new Chart(ctx, {
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
  options: {
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
      },
    },
    animation: false,
  },
});

function getRandomInt(max) {
  return Math.floor(Math.random() * max);
}

for (let i = 0; i < 50; i++) {
  setTimeout(() => {
    const timestamp = dateFormatter(new Date());
    if (lineChart.data.labels.length > 10) {
      lineChart.data.labels.shift();
      lineChart.data.datasets[0].data.shift();
    }
    lineChart.data.datasets[0].data.push(getRandomInt(100));
    lineChart.data.labels.push(timestamp);

    lineChart.update();
  }, 1000 + i * 500);
}
