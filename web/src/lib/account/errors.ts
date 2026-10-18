import { m } from '$lib/paraglide/messages';

const MESSAGES: Record<string, () => string> = {
	unauthorized: () => m.account_err_unauthorized(),
	rate_limited: () => m.account_err_rate_limited(),
	network: () => m.account_err_network(),
	server_busy: () => m.account_err_server_busy()
};

/** The translated sentence for a failed account request (anything unexpected is a generic one). */
export function accountErrorMessage(code: string): string {
	return (MESSAGES[code] ?? (() => m.account_err_unknown()))();
}
