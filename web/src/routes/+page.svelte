<script lang="ts">
	import { onMount, untrack } from 'svelte';
	import { afterNavigate, goto, replaceState } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import { account, accountErrorMessage } from '$lib/account';
	import AccountChip from '$lib/account/AccountChip.svelte';
	import GoogleButton from '$lib/account/GoogleButton.svelte';
	import LegalLinks from '$lib/components/LegalLinks.svelte';
	import SupportLink from '$lib/components/SupportLink.svelte';
	import { ApiError, api, type RoomInfo } from '$lib/api';
	import AvatarBadge from '$lib/components/AvatarBadge.svelte';
	import AvatarPicker from '$lib/components/AvatarPicker.svelte';
	import Button from '$lib/components/Button.svelte';
	import LanguageSwitch from '$lib/components/LanguageSwitch.svelte';
	import RoomCodeInput from '$lib/components/RoomCodeInput.svelte';
	import Wordmark from '$lib/components/Wordmark.svelte';
	import { errorMessage } from '$lib/errors';
	import { markRoom } from '$lib/game/roomMark';
	import { i18n } from '$lib/i18n.svelte';
	import { capNickname, NICKNAME_MAX, normalizeNickname } from '$lib/nickname';
	import { m } from '$lib/paraglide/messages';
	import { profile } from '$lib/profile.svelte';
	import { isCompleteRoomCode } from '$lib/roomCode';
	import LegalButton from '$lib/safety/LegalButton.svelte';
	import { DECLINED, terms } from '$lib/safety/terms.svelte';
	import { toasts } from '$lib/toast.svelte';

	let draft = $state(profile.nickname);
	let code = $state('');
	let visibility = $state<'private' | 'public'>('private');
	/** The action in flight: its button shows progress and everything else is disabled meanwhile. */
	let busy = $state<'create' | 'join' | null>(null);

	let signingOut = $state(false);

	const canCreate = $derived(profile.nickname.length > 0 && busy === null);
	const canJoin = $derived(canCreate && isCompleteRoomCode(code));

	function onNicknameInput() {
		draft = capNickname(draft.trimStart());
		profile.setNickname(draft);
	}

	// The saved profile of a signed-in user is adopted into the shared store after load: follow it.
	$effect(() => {
		const saved = profile.nickname;
		if (normalizeNickname(untrack(() => draft)) !== saved) draft = saved;
	});

	onMount(() => void account.init());

	// `/?login=failed|cancelled`: the OAuth callback could not sign the user in. Say so once, then
	// clean the URL (the router is only ready after the first navigation).
	afterNavigate(() => {
		visibility = page.url.searchParams.get('visibility') === 'public' ? 'public' : 'private';
		const reason = page.url.searchParams.get('login');
		if (reason !== 'failed' && reason !== 'cancelled') return;
		toasts.show(reason === 'failed' ? m.account_login_failed() : m.account_login_cancelled());
		const rest = new URL(page.url);
		rest.searchParams.delete('login');
		// eslint-disable-next-line svelte/no-navigation-without-resolve -- resolved path plus the remaining query/hash
		replaceState(resolve('/') + rest.search + rest.hash, page.state);
	});

	async function signOut() {
		if (signingOut) return;
		signingOut = true;
		const result = await account.logout();
		signingOut = false;
		toasts.show(result.ok ? m.account_signed_out() : accountErrorMessage(result.code));
	}

	function notJoinable(info: RoomInfo): string {
		if (info.phase === 'final') return m.home_game_over();
		if (info.players >= info.maxPlayers) return m.home_room_full();
		return m.home_not_joinable();
	}

	async function enter(target: string) {
		markRoom(target);
		await goto(resolve('/[code=roomcode]', { code: target }));
	}

	async function create() {
		if (!canCreate) return;
		busy = 'create';
		try {
			// The terms come first; a 403 terms_required (a stale idea of the state) opens the gate and retries once.
			const created = await terms.run(() =>
				api.createRoom({
					name: profile.nickname,
					avatar: profile.avatar,
					lang: i18n.locale,
					visibility
				})
			);
			if (created !== DECLINED) await enter(created.code);
		} catch (e) {
			toasts.show(errorMessage(e instanceof ApiError ? e.code : 'unknown'));
		} finally {
			busy = null;
		}
	}

	async function join() {
		if (!canJoin) return;
		busy = 'join';
		try {
			const info = await api.getRoom(code);
			if (!info.joinable) toasts.show(notJoinable(info));
			else if (await terms.ensure()) await enter(info.code);
		} catch (e) {
			toasts.show(errorMessage(e instanceof ApiError ? e.code : 'unknown'));
		} finally {
			busy = null;
		}
	}
</script>

{#snippet pending(label: string)}
	<span class="flex items-center justify-center gap-3">
		<span
			class="size-5 animate-spin rounded-full border-[3px] border-night/30 border-t-night"
			aria-hidden="true"
		></span>
		{label}
	</span>
{/snippet}

<!--
	Mobile-first, thumb-friendly: everything needed to start a game fits in one
	screen (390x844 and 360x740, no scrolling). The identity (logo, nickname,
	avatar) sits on top and the actions are anchored to the bottom.
-->
<main
	class="mx-auto flex min-h-dvh w-full max-w-md flex-col gap-2 px-3 pt-[max(0.5rem,env(safe-area-inset-top))] pb-[max(0.75rem,env(safe-area-inset-bottom))] tall:gap-4"
>
	<header class="flex flex-col items-center gap-1 text-center">
		<div class="flex w-full items-center justify-between gap-2">
			<AccountChip />
			<div class="ml-auto flex items-center gap-2">
				<LegalButton />
				<LanguageSwitch />
			</div>
		</div>
		<Wordmark />
		<p class="max-w-xs text-base leading-snug text-cream-muted tall:text-lg">{m.tagline()}</p>
	</header>

	<section class="flex flex-col gap-2 rounded-3xl bg-night-2/80 p-2 ring-1 ring-night-3">
		<div class="flex items-end gap-2">
			<AvatarBadge avatar={profile.avatar} size={48} />
			<div class="flex min-w-0 flex-1 flex-col gap-0.5">
				<div class="flex items-baseline justify-between gap-2 px-1">
					<label for="nickname" class="text-sm font-bold text-cream-muted">
						{m.nickname_label()}
					</label>
					<span id="nickname-hint" class="text-xs text-cream-muted">
						{m.nickname_hint({ max: NICKNAME_MAX })}
					</span>
				</div>
				<input
					id="nickname"
					name="nickname"
					type="text"
					bind:value={draft}
					oninput={onNicknameInput}
					onblur={() => (draft = profile.nickname)}
					placeholder={m.nickname_placeholder()}
					aria-describedby="nickname-hint"
					autocomplete="off"
					autocapitalize="words"
					enterkeyhint="done"
					spellcheck="false"
					class="h-12 w-full rounded-xl bg-night px-4 text-lg font-bold text-cream ring-2 ring-night-3 placeholder:font-normal placeholder:text-cream-muted/70 focus:ring-banana focus:outline-none"
				/>
			</div>
		</div>

		<AvatarPicker avatar={profile.avatar} onchange={(next) => profile.setAvatar(next)} />
	</section>

	<section class="mt-auto flex flex-col gap-2 tall:gap-3">
		<div class="grid grid-cols-2 items-center gap-2">
			<div
				role="group"
				aria-label={m.rooms_visibility()}
				class="grid grid-cols-2 gap-1 rounded-2xl bg-night-2 p-1"
			>
				{#each ['private', 'public'] as option (option)}
					<button
						type="button"
						disabled={busy !== null}
						aria-pressed={visibility === option}
						onclick={() => (visibility = option as 'private' | 'public')}
						class="h-12 rounded-xl text-sm font-bold {visibility === option
							? 'bg-night-3 text-banana'
							: 'text-cream-muted'}"
					>
						{option === 'private' ? m.rooms_private() : m.rooms_public()}
					</button>
				{/each}
			</div>
			<Button
				tone="banana"
				size="md"
				class="h-14! px-2! leading-tight"
				disabled={!canCreate && busy !== 'create'}
				aria-busy={busy === 'create'}
				onclick={create}
			>
				{#if busy === 'create'}
					{@render pending(m.home_create_pending())}
				{:else}
					{m.create_game()}
				{/if}
			</Button>
		</div>
		{#if visibility === 'public'}
			<p class="text-center text-xs leading-tight text-cream-muted">{m.rooms_casual()}</p>
		{/if}

		<div class="flex flex-col gap-1">
			<div class="flex flex-wrap items-baseline justify-between gap-x-2 px-1" aria-hidden="true">
				<span class="text-sm font-bold text-cream-muted">{m.code_label()}</span>
				<span id="code-hint" class="text-xs text-cream-muted">{m.code_hint()}</span>
			</div>
			<RoomCodeInput bind:value={code} describedby="code-hint" disabled={busy !== null} />
		</div>

		<div class="grid grid-cols-2 gap-2">
			<Button
				tone="strawberry"
				size="md"
				class="h-14! px-2! leading-tight"
				disabled={!canJoin && busy !== 'join'}
				aria-busy={busy === 'join'}
				onclick={join}
			>
				{#if busy === 'join'}
					{@render pending(m.home_join_pending())}
				{:else}
					{m.join_game()}
				{/if}
			</Button>
			<button
				type="button"
				disabled={busy !== null}
				onclick={() => goto(resolve('/rooms'))}
				class="h-14 rounded-2xl bg-night-3 px-2 leading-tight font-bold text-cream disabled:opacity-50"
			>
				{m.rooms_browse()}
			</button>
		</div>

		<!-- Account slot: reserved while the session loads so nothing jumps; empty when Google is off. -->
		{#if account.status === 'loading'}
			<div class="h-12" aria-hidden="true"></div>
		{:else if account.loggedIn}
			<div class="grid grid-cols-2 gap-2">
				<a
					href={resolve('/stats')}
					class="flex h-12 items-center justify-center rounded-2xl px-2 text-center font-bold text-cream ring-1 ring-night-3 transition active:bg-night-3"
				>
					{m.account_stats_link()}
				</a>
				<button
					type="button"
					aria-busy={signingOut}
					onclick={signOut}
					class="flex h-12 items-center justify-center rounded-2xl px-2 font-bold text-cream ring-1 ring-night-3 transition active:bg-night-3"
				>
					{signingOut ? m.account_sign_out_pending() : m.account_sign_out()}
				</button>
			</div>
		{:else if account.googleEnabled}
			<GoogleButton returnTo="/" />
		{/if}
		<div class="-mt-2 flex flex-wrap items-center justify-center gap-x-2">
			<LegalLinks pages={['privacy']} />
			<SupportLink />
		</div>
	</section>
</main>
