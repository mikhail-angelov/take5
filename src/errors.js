export class CliUsageError extends Error {
  constructor(message) {
    super(message);
    this.name = 'CliUsageError';
  }
}

export class CliOperationError extends Error {
  constructor(message) {
    super(message);
    this.name = 'CliOperationError';
  }
}
