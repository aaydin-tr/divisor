const dateFormatter = (date) => {
  const hours = date.getHours();
  const minute = date.getMinutes();
  const second = date.getSeconds();

  return hours + ":" + minute + ":" + second;
};
