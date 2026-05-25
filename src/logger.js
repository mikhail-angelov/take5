export function createLogger({ debug = false } = {}) {
  return {
    info(message) {
      console.log(message);
    },
    debug(message) {
      if (debug) {
        console.log(message);
      }
    },
    error(message) {
      console.error(message);
    },
  };
}
