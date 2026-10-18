<script lang="ts">
	import { onMount, tick, untrack } from 'svelte';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { account, accountErrorMessage, averagePoints, winRate } from '$lib/account';
	import AccountChip from '$lib/account/AccountChip.svelte';
	import GoogleButton from '$lib/account/GoogleButton.svelte';
	import Button from '$lib/components/Button.svelte';
	import LanguageSwitch from '$lib/components/LanguageSwitch.svelte';
	import PrivacyLink from '$lib/components/PrivacyLink.svelte';
	import Screen from '$lib/components/Screen.svelte';
	import { BRAND } from '$lib/brand';
	import { i18n } from '$lib/i18n.svelte';
	import { m } from '$lib/paraglide/messages';
	import { toasts } from '$lib/toast.svelte';
	import DeleteDialog from './DeleteDialog.svelte';
	import StatTile from './StatTile.svelte';

	let confirming = $state(false);
	let deleting = $state(false);
	let deleteError = $state<string | null>(null);
	let signingOut = $state(false);
	let deleteTrigger = $state<HTMLButtonElement>();

	onMount(() => void account.init());

	// Fresh numbers every time the screen opens (the previous ones stay visible meanwhile).
	$effect(() => {
		if (account.status === 'ready' && account.loggedIn) untrack(() => void account.loadStats());
	});

	// Whatever ends the session behind the dialog's back (expired cookie) also closes it.
	$effect(() => {
		if (!account.loggedIn) confirming = false;
	});

	const view = $derived.by(() => {
		if (account.status === 'loading') return 'loading';
		if (!account.loggedIn) return account.initError !== null ? 'error' : 'login';
		if (account.statsState === 'error') return 'error';
		const stats = account.stats;
		if (!stats) return 'loading';
		return stats.gamesPlayed === 0 ? 'empty' : 'data';
	});

	const errorCode = $derived(account.loggedIn ? account.statsError : account.initError);
	const stats = $derived(account.stats);

	const number = (n: number, digits = 0) =>
		n.toLocaleString(i18n.locale, { maximumFractionDigits: digits });

	const announcement = $derived(
		{
			loading: m.stats_loading(),
			data: m.stats_loaded(),
			empty: m.stats_empty_title(),
			error: m.stats_load_error_title(),
			login: m.stats_login_title()
		}[view]
	);

	// Back to the button that opened the dialog, once the page behind it is interactive again.
	function closeDialog() {
		confirming = false;
		void tick().then(() => deleteTrigger?.focus());
	}

	function retry() {
		void (account.loggedIn ? account.loadStats() : account.init());
	}

	async function signOut() {
		if (signingOut) return;
		signingOut = true;
		const result = await account.logout();
		signingOut = false;
		if (!result.ok) return void toasts.show(accountErrorMessage(result.code));
		toasts.show(m.account_signed_out());
		await goto(resolve('/'));
	}

	async function confirmDelete() {
		if (deleting) return;
		deleting = true;
		deleteError = null;
		const result = await account.deleteAccount();
		deleting = false;
		if (result.ok) {
			confirming = false;
			toasts.show(m.stats_deleted());
			await goto(resolve('/'));
		} else if (result.code === 'unauthorized') {
			toasts.show(accountErrorMessage(result.code));
		} else {
			deleteError = accountErrorMessage(result.code);
		}
	}

	const homeLink =
		'flex h-14 w-full items-center justify-center rounded-2xl bg-banana px-6 text-xl font-black text-night shadow-pop transition active:translate-y-1 active:shadow-none';
</script>

<svelte:head>
	<title>{BRAND} · {m.stats_title()}</title>
</svelte:head>

<div inert={confirming}>
	<Screen label={m.stats_title()}>
		{#snippet header()}
			<div class="flex w-full items-center justify-between gap-2">
				<AccountChip />
				<div class="ml-auto">
					<LanguageSwitch />
				</div>
			</div>
		{/snippet}

		<h1 class="text-3xl leading-tight font-black">{m.stats_title()}</h1>
		<p class="sr-only" role="status">{announcement}</p>

		{#if view === 'loading'}
			<div class="grid grid-cols-2 gap-3" aria-busy="true">
				{#each [0, 1, 2, 3, 4] as i (i)}
					<div
						class="h-28 animate-pulse rounded-3xl bg-night-2/80 ring-1 ring-night-3 {i === 4
							? 'col-span-2'
							: ''}"
					></div>
				{/each}
			</div>
		{:else if view === 'login'}
			<section
				class="flex flex-col items-center gap-3 rounded-3xl bg-night-2/80 p-4 text-center ring-1 ring-night-3"
			>
				<span class="text-6xl" aria-hidden="true">🔒</span>
				<h2 class="text-2xl leading-tight font-black">{m.stats_login_title()}</h2>
				{#if account.googleEnabled}
					<p class="text-lg text-cream-muted">{m.stats_login_text()}</p>
					<GoogleButton variant="primary" returnTo="/" />
				{:else}
					<p class="text-lg text-cream-muted">{m.stats_login_unavailable()}</p>
				{/if}
			</section>
		{:else if view === 'error'}
			<section
				class="flex flex-col items-center gap-3 rounded-3xl bg-night-2/80 p-4 text-center ring-1 ring-night-3"
			>
				<span class="text-6xl" aria-hidden="true">📡</span>
				<h2 class="text-2xl leading-tight font-black">{m.stats_load_error_title()}</h2>
				<p class="text-lg text-cream-muted">{accountErrorMessage(errorCode ?? 'unknown')}</p>
				<Button tone="banana" size="md" onclick={retry}>{m.stats_retry()}</Button>
			</section>
		{:else if view === 'empty'}
			<section
				class="flex flex-col items-center gap-3 rounded-3xl bg-night-2/80 p-4 text-center ring-1 ring-night-3"
			>
				<span class="text-6xl" aria-hidden="true">🍓</span>
				<h2 class="text-2xl leading-tight font-black">{m.stats_empty_title()}</h2>
				<p class="text-lg text-cream-muted">{m.stats_empty_text()}</p>
			</section>
		{:else if stats}
			{@const rate = winRate(stats)}
			{@const average = averagePoints(stats)}
			<div class="grid grid-cols-2 gap-3">
				<StatTile icon="🎮" label={m.stats_games()} value={number(stats.gamesPlayed)} />
				<StatTile
					icon="🏆"
					label={m.stats_wins()}
					value={number(stats.wins)}
					hint={rate === null ? undefined : m.stats_win_rate({ rate: number(rate) })}
					tone="text-lime"
				/>
				<StatTile
					icon="⭐"
					label={m.stats_points()}
					value={number(stats.totalPoints)}
					hint={average === null ? undefined : m.stats_avg_points({ avg: number(average, 1) })}
					tone="text-tangerine"
				/>
				<StatTile
					icon="🔥"
					label={m.stats_best_round()}
					value={number(stats.bestRound)}
					tone="text-strawberry"
				/>
				<StatTile
					icon="💎"
					label={m.stats_unique()}
					value={number(stats.uniqueAnswers)}
					tone="text-blueberry"
					wide
				/>
			</div>
		{/if}

		{#if account.loggedIn}
			<section class="mt-2 flex flex-col gap-2" aria-labelledby="account-heading">
				<h2 id="account-heading" class="px-1 text-sm font-bold text-cream-muted">
					{m.stats_account_heading()}
				</h2>
				<button
					type="button"
					aria-busy={signingOut}
					onclick={signOut}
					class="flex h-12 items-center justify-center rounded-2xl px-4 font-bold text-cream ring-1 ring-night-3 transition active:bg-night-3"
				>
					{signingOut ? m.account_sign_out_pending() : m.account_sign_out()}
				</button>
				<button
					bind:this={deleteTrigger}
					type="button"
					onclick={() => {
						deleteError = null;
						confirming = true;
					}}
					class="flex h-12 items-center justify-center rounded-2xl px-4 font-bold text-strawberry ring-1 ring-strawberry/60 transition active:bg-night-3"
				>
					{m.stats_delete()}
				</button>
				<PrivacyLink />
			</section>
		{/if}

		{#snippet footer()}
			<a href={resolve('/')} class={homeLink}>
				{view === 'empty' ? m.stats_play() : m.stats_back_home()}
			</a>
		{/snippet}
	</Screen>
</div>

{#if confirming}
	<DeleteDialog
		busy={deleting}
		error={deleteError}
		onconfirm={confirmDelete}
		oncancel={closeDialog}
	/>
{/if}
