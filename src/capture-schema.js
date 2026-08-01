// Single source of truth lives in extension/ so the Chrome extension package is
// self-contained at runtime; the CLI re-exports the same contract.
export { validateCaptureBundle } from '../extension/capture-schema.js';
