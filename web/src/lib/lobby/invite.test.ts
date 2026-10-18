// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { i18n } from '$lib/i18n.svelte';
import { copyText, inviteUrl, shareInvite } from './invite';

function setNavigator(props: { share?: unknown; clipboard?: unknown }) {
	for (const [key, value] of Object.entries(props)) {
		Object.defineProperty(navigator, key, { value, configurable: true });
	}
}

describe('invite', () => {
	beforeEach(() => i18n.set('es'));
	afterEach(() => {
		setNavigator({ share: undefined, clipboard: undefined });
		vi.restoreAllMocks();
	});

	it('builds the invite link from the origin and the code', () => {
		expect(inviteUrl('KT7P', 'https://tuttifrutti.aresius.xyz')).toBe(
			'https://tuttifrutti.aresius.xyz/KT7P'
		);
		expect(inviteUrl('KT7P')).toBe(`${location.origin}/KT7P`);
	});

	describe('copyText', () => {
		it('uses the async clipboard', async () => {
			const writeText = vi.fn().mockResolvedValue(undefined);
			setNavigator({ clipboard: { writeText } });
			expect(await copyText('KT7P')).toBe(true);
			expect(writeText).toHaveBeenCalledWith('KT7P');
		});

		it('falls back to execCommand when the clipboard API is missing or refuses', async () => {
			const exec = vi.fn().mockReturnValue(true);
			document.execCommand = exec;
			setNavigator({ clipboard: undefined });
			expect(await copyText('x')).toBe(true);
			setNavigator({ clipboard: { writeText: vi.fn().mockRejectedValue(new Error('denied')) } });
			expect(await copyText('y')).toBe(true);
			expect(exec).toHaveBeenCalledWith('copy');
			expect(document.querySelector('textarea')).toBeNull();
		});

		it('reports failure instead of throwing', async () => {
			setNavigator({ clipboard: undefined });
			document.execCommand = vi.fn().mockReturnValue(false);
			expect(await copyText('x')).toBe(false);
			document.execCommand = vi.fn().mockImplementation(() => {
				throw new Error('nope');
			});
			expect(await copyText('x')).toBe(false);
		});
	});

	describe('shareInvite', () => {
		it('shares title, text and url with the Web Share API', async () => {
			const share = vi.fn().mockResolvedValue(undefined);
			setNavigator({ share });
			expect(await shareInvite('KT7P')).toBe('shared');
			expect(share).toHaveBeenCalledWith({
				title: 'Tutti Frutti',
				text: '¡Jugá Tutti Frutti conmigo! Código KT7P',
				url: `${location.origin}/KT7P`
			});
		});

		it('does nothing more when the person closes the share sheet', async () => {
			const writeText = vi.fn();
			setNavigator({
				share: vi.fn().mockRejectedValue(new DOMException('closed', 'AbortError')),
				clipboard: { writeText }
			});
			expect(await shareInvite('KT7P')).toBe('cancelled');
			expect(writeText).not.toHaveBeenCalled();
		});

		it('copies text and link when sharing fails for another reason or does not exist', async () => {
			const writeText = vi.fn().mockResolvedValue(undefined);
			setNavigator({
				share: vi.fn().mockRejectedValue(new Error('NotAllowedError')),
				clipboard: { writeText }
			});
			expect(await shareInvite('KT7P')).toBe('copied');
			expect(writeText).toHaveBeenCalledWith(
				`¡Jugá Tutti Frutti conmigo! Código KT7P ${location.origin}/KT7P`
			);
			setNavigator({ share: undefined });
			expect(await shareInvite('KT7P')).toBe('copied');
			expect(writeText).toHaveBeenCalledTimes(2);
		});

		it('says so when there is nothing to fall back to', async () => {
			setNavigator({ share: undefined, clipboard: undefined });
			document.execCommand = vi.fn().mockReturnValue(false);
			expect(await shareInvite('KT7P')).toBe('failed');
		});
	});
});
