import { m } from '$lib/paraglide/messages';
import type { UpdateError } from './update.svelte';

/** The translated sentence for an update problem. */
export function updateErrorText(error: UpdateError): string {
	switch (error) {
		case 'offline':
			return m.update_offline();
		case 'not_ready':
			return m.update_not_ready();
		case 'failed':
			return m.update_failed();
	}
}
