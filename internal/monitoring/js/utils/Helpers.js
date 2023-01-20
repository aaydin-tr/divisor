const dateFormatter = (date) => {
  const hours = date.getHours();
  const minute = date.getMinutes();
  const second = date.getSeconds();

  return hours + ":" + minute + ":" + second;
};

const setLocalTheme = (data) => {
  return localStorage.setItem("theme", data);
};

const getLocalTheme = (key) => {
  return localStorage.getItem(key);
};
