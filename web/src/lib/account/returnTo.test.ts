import { describe, expect, it } from 'vitest';
import { loginPath, safeReturnTo } from './returnTo';

describe('safeReturnTo (same allow-list as the server)', () => {
	it.each([
		['/', '/'],
		['/KT7P', '/KT7P'],
		['/kt7p', '/KT7P'],
		['/bcdf', '/BCDF'],
		['/2345', '/2345']
	])('accepts %s as %s', (input, expected) => {
		expect(safeReturnTo(input)).toBe(expected);
	});

	it.each([
		'',
		'KT7P',
		'/KT7',
		'/KT7PX',
		'/KT7P/',
		'/KT7P?x=1',
		'/KT7P#x',
		'/AEIO', // vowels are not in the alphabet
		'/KT70', // 0 and 1 are not either
		'/stats',
		'//evil.example',
		'/\\evil.example',
		'https://evil.example/KT7P',
		'javascript:alert(1)',
		'/ßßßß', // Unicode upper-casing must not produce "SSSS"
		' /KT7P',
		'/KT7P\n'
	])('turns %j into /', (input) => {
		expect(safeReturnTo(input)).toBe('/');
	});

	it('turns non-strings into /', () => {
		for (const value of [undefined, null, 42, {}, ['/KT7P']]) expect(safeReturnTo(value)).toBe('/');
	});
});

describe('loginPath', () => {
	it('encodes the sanitised return path', () => {
		expect(loginPath('/')).toBe('/api/auth/google/login?returnTo=%2F');
		expect(loginPath('/kt7p')).toBe('/api/auth/google/login?returnTo=%2FKT7P');
		expect(loginPath('//evil.example')).toBe('/api/auth/google/login?returnTo=%2F');
		expect(loginPath(undefined)).toBe('/api/auth/google/login?returnTo=%2F');
	});
});
