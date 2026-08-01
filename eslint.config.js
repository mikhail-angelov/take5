import js from '@eslint/js';
import globals from 'globals';
import prettier from 'eslint-config-prettier';

export default [
  {
    ignores: ['node_modules/**', 'take5-output/**', 'take5-output-test/**', '.worktrees/**'],
  },
  js.configs.recommended,

  // CLI + scripts: Node ESM.
  {
    files: ['src/**/*.js', 'scripts/**/*.js'],
    languageOptions: {
      ecmaVersion: 2023,
      sourceType: 'module',
      globals: { ...globals.node },
    },
  },

  // Extension runtime: browser + chrome APIs, ES modules.
  {
    files: ['extension/**/*.js'],
    languageOptions: {
      ecmaVersion: 2023,
      sourceType: 'module',
      globals: { ...globals.browser, chrome: 'readonly' },
    },
  },

  // content-script.js must stay a classic (non-module) script.
  {
    files: ['extension/content-script.js'],
    languageOptions: { sourceType: 'script' },
  },

  // Tests run under `node --test` regardless of directory.
  {
    files: ['**/*.test.js'],
    languageOptions: {
      sourceType: 'module',
      globals: { ...globals.node },
    },
  },

  prettier,
];
