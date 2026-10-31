import js from '@eslint/js';
import playwright from 'eslint-plugin-playwright';
import globals from 'globals';
import tseslint from 'typescript-eslint';

export default tseslint.config(
	{ ignores: ['node_modules/', 'playwright-report/', 'test-results/'] },
	js.configs.recommended,
	...tseslint.configs.recommended,
	{
		files: ['**/*.ts'],
		languageOptions: { globals: { ...globals.node, ...globals.browser } },
		rules: {
			'@typescript-eslint/no-floating-promises': 'off'
		}
	},
	{
		...playwright.configs['flat/recommended'],
		files: ['tests/**/*.ts', 'src/**/*.ts', 'store/**/*.ts'],
		rules: {
			...playwright.configs['flat/recommended'].rules,
			'playwright/expect-expect': 'off',
			'playwright/no-conditional-in-test': 'off',
			'playwright/no-conditional-expect': 'off',
			'playwright/no-wait-for-timeout': 'error',
			'playwright/missing-playwright-await': 'error',
			'playwright/no-force-option': 'error'
		}
	}
);
