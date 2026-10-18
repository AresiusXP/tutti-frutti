import { BRAND } from '$lib/brand';
import { m } from '$lib/paraglide/messages';

/** The link a friend opens to join: `https://<host>/<CODE>`. */
export function inviteUrl(code: string, origin: string = location.origin): string {
	return `${origin}/${code}`;
}

/** Copies text to the clipboard; false when the browser refuses (never throws). */
export async function copyText(text: string): Promise<boolean> {
	try {
		if (navigator.clipboard?.writeText) {
			await navigator.clipboard.writeText(text);
			return true;
		}
	} catch {
		// fall through to the legacy path (insecure origins, permission policy)
	}
	try {
		const area = document.createElement('textarea');
		area.value = text;
		area.setAttribute('readonly', '');
		area.style.cssText = 'position:fixed;top:0;left:0;opacity:0;';
		document.body.appendChild(area);
		area.select();
		const ok = document.execCommand('copy');
		area.remove();
		return ok;
	} catch {
		return false;
	}
}

export type ShareOutcome = 'shared' | 'copied' | 'cancelled' | 'failed';

/**
 * Shares the invite with the Web Share API. Without it, or when it fails for
 * a reason other than the user closing the sheet, the invite text and link are
 * copied instead.
 */
export async function shareInvite(code: string): Promise<ShareOutcome> {
	const url = inviteUrl(code);
	const text = m.share_text({ brand: BRAND, code });
	if (typeof navigator.share === 'function') {
		try {
			await navigator.share({ title: BRAND, text, url });
			return 'shared';
		} catch (error) {
			if ((error as { name?: string } | null)?.name === 'AbortError') return 'cancelled';
		}
	}
	return (await copyText(`${text} ${url}`)) ? 'copied' : 'failed';
}
