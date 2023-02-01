const dateFormatter = (date) => {
    const hours = date.getHours();
    const minute = date.getMinutes();
    const second = date.getSeconds();

    return hours + ":" + minute + ":" + second;
};

const getRandomInt = (max) => {
    return Math.floor(Math.random() * max);
}

const cpu = document.getElementById("cpu");
const ram = document.getElementById("ram");

const generalOption = {
    // animations: {
    //     tension: {
    //         easing: 'easeInOutBack',
    //         loop: false
    //     }
    // },
    animations:false,
    elements: {
        point: {
            radius: 0.2,
            hitRadius: 20,
        }
    }
}

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
            }
        ],
    },
    options: {
        ...generalOption,
        scales: {
            y: {
                min: 0,
                max: 100,
                beginAtZero: true,
            },
        }
    },
});

const ramChart = new Chart(ram, {
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
    options: {
        ...generalOption,
        scales: {
            y: {
                min: 0,
                max: 100,
                beginAtZero: true,
            },
        }
    },
});


const charts = [cpuChart, ramChart];

setInterval(() => {
    fetchDatas()
}, 1000);


const fetchDatas = () => {
    fetch("http://localhost:8000/")
        .then((res) => res.json())
        .then(({ cpu, memory }) => {
            const timestamp = dateFormatter(new Date());
            console.log({ cpu, memory })
            charts.forEach(chart => {
                if (chart.data.labels.length > 5) {
                    chart.data.datasets.forEach(function (dataset) { dataset.data.shift(); });
                    chart.data.labels.shift();
                }
                chart.data.labels.push(timestamp);
            });

            cpuChart.data.datasets[0].data.push(cpu.process_percent);
            cpuChart.data.datasets[1].data.push(cpu.total_percent);


            console.log(cpuChart.data.datasets)
            cpuChart.update();
        })
}
