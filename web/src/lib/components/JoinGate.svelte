<script lang="ts">
	import { onMount, untrack } from 'svelte';
	import { resolve } from '$app/paths';
	import { account } from '$lib/account';
	import AccountChip from '$lib/account/AccountChip.svelte';
	import GoogleButton from '$lib/account/GoogleButton.svelte';
	import { ApiError, api, type RoomInfo } from '$lib/api';
	import AvatarBadge from '$lib/components/AvatarBadge.svelte';
	import AvatarPicker from '$lib/components/AvatarPicker.svelte';
	import Button from '$lib/components/Button.svelte';
	import LanguageSwitch from '$lib/components/LanguageSwitch.svelte';
	import Screen from '$lib/components/Screen.svelte';
	import Wordmark from '$lib/components/Wordmark.svelte';
	import { capNickname, NICKNAME_MAX, normalizeNickname } from '$lib/nickname';
	import { errorMessage } from '$lib/errors';
	import { m } from '$lib/paraglide/messages';
	import { profile } from '$lib/profile.svelte';

	/**
	 * What a visitor sees when opening an invite link (`/KT7P`) in a tab that
	 * has not chosen to be in the room yet: the room is checked first, then
	 * nickname + avatar and a big Join button. `onjoin` is called once the
	 * player pressed Join (the route then marks the tab and connects).
	 */
	let {
		code,
		onjoin,
		load = (c: string) => api.getRoom(c)
	}: {
		code: string;
		onjoin: () => void;
		/** Room preflight; replaced in tests. */
		load?: (code: string) => Promise<RoomInfo>;
	} = $props();

	type Check =
		| { kind: 'loading' }
		| { kind: 'ready'; info: RoomInfo }
		| { kind: 'full' | 'over'; info: RoomInfo }
		| { kind: 'not_found' | 'unavailable' | 'denied' };

	let check = $state<Check>({ kind: 'loading' });
	let draft = $state(profile.nickname);
	let attempt = 0;

	async function run() {
		const mine = ++attempt;
		check = { kind: 'loading' };
		try {
			const info = await load(code);
			if (mine !== attempt) return;
			if (info.joinable) check = { kind: 'ready', info };
			else if (info.phase === 'final') check = { kind: 'over', info };
			else if (info.players >= info.maxPlayers) check = { kind: 'full', info };
			// not joinable although there is room: the server is draining, try again in a moment
			else check = { kind: 'unavailable' };
		} catch (e) {
			if (mine !== attempt) return;
			// A safety refusal (a block, a restriction) is shown as vaguely as the server says it.
			check = {
				kind:
					e instanceof ApiError && e.status === 404
						? 'not_found'
						: e instanceof ApiError && e.code === 'join_unavailable'
							? 'denied'
							: 'unavailable'
			};
		}
	}
	onMount(() => {
		void run();
		void account.init();
		return () => attempt++;
	});

	// The saved profile of a signed-in user is adopted into the shared store after load: follow it.
	$effect(() => {
		const saved = profile.nickname;
		if (normalizeNickname(untrack(() => draft)) !== saved) draft = saved;
	});

	const canJoin = $derived(profile.nickname.length > 0);

	function onNicknameInput() {
		draft = capNickname(draft.trimStart());
		profile.setNickname(draft);
	}

	function join() {
		if (canJoin) onjoin();
	}

	const homeLink =
		'flex h-14 w-full items-center justify-center rounded-2xl px-6 text-xl font-black shadow-pop transition active:translate-y-1 active:shadow-none';
</script>

{#snippet blocked(icon: string, title: string, text: string, retry: boolean, again: boolean)}
	<Screen label={title}>
		<div class="flex flex-1 flex-col items-center justify-center gap-4 text-center">
			<span class="animate-bounce-in text-7xl" aria-hidden="true">{icon}</span>
			<h1 class="text-3xl leading-tight font-black">{title}</h1>
			<p class="max-w-xs text-lg text-cream-muted">{text}</p>
			{#if 'info' in check && check.info.visibility === 'public'}
				<p class="text-sm text-cream-muted">{m.rooms_casual()}</p>
			{/if}
		</div>
		{#snippet footer()}
			{#if retry}
				<Button tone="banana" onclick={run}>{m.gate_retry()}</Button>
			{/if}
			{#if again && canJoin}
				<Button tone="plain" size="md" onclick={join}>{m.gate_try_anyway()}</Button>
			{/if}
			<a
				href={resolve('/')}
				class="{homeLink} {retry ? 'bg-night-3 text-cream' : 'bg-banana text-night'}"
			>
				{m.net_home()}
			</a>
		{/snippet}
	</Screen>
{/snippet}

{#if check.kind === 'not_found'}
	{@render blocked('🔍', m.gate_not_found_title(), m.gate_not_found_text(), false, false)}
{:else if check.kind === 'full'}
	{@render blocked(
		'🈵',
		m.gate_full_title(),
		m.gate_full_text({ max: check.info.maxPlayers }),
		false,
		true
	)}
{:else if check.kind === 'over'}
	{@render blocked('🏁', m.gate_over_title(), m.gate_over_text(), false, true)}
{:else if check.kind === 'denied'}
	{@render blocked('🙈', m.closed_rejected_title(), errorMessage('join_unavailable'), false, false)}
{:else if check.kind === 'unavailable'}
	{@render blocked('📡', m.gate_unavailable_title(), m.gate_unavailable_text(), true, false)}
{:else}
	<Screen label={m.gate_title()}>
		{#snippet header()}
			<div class="flex w-full items-center justify-between gap-2">
				{#if account.status === 'ready'}
					{#if account.loggedIn}
						<AccountChip />
					{:else if account.googleEnabled}
						<GoogleButton variant="compact" returnTo="/{code}" />
					{/if}
				{/if}
				<div class="ml-auto">
					<LanguageSwitch />
				</div>
			</div>
			<Wordmark />
		{/snippet}

		<div class="flex flex-col items-center gap-1 text-center">
			<h2 class="text-xl font-bold text-cream-muted">{m.gate_title()}</h2>
			<p
				class="rounded-2xl bg-night-2 px-6 py-1 text-5xl font-black tracking-[0.2em] text-banana ring-1 ring-night-3"
				aria-label={m.gate_code_aria({ code })}
			>
				{code}
			</p>
			<p class="min-h-6 text-sm text-cream-muted" role="status">
				{#if check.kind === 'ready'}
					{m.gate_players({ count: check.info.players, max: check.info.maxPlayers })}
				{:else}
					{m.gate_checking()}
				{/if}
			</p>
		</div>

		{#if check.kind === 'ready' && check.info.visibility === 'public'}
			<p class="text-center text-sm text-cream-muted">{m.rooms_casual()}</p>
		{/if}
		<section class="flex flex-col gap-2 rounded-3xl bg-night-2/80 p-2 ring-1 ring-night-3">
			<div class="flex items-end gap-2">
				<AvatarBadge avatar={profile.avatar} size={48} />
				<div class="flex min-w-0 flex-1 flex-col gap-0.5">
					<div class="flex items-baseline justify-between gap-2 px-1">
						<label for="gate-nickname" class="text-sm font-bold text-cream-muted">
							{m.gate_nickname_label()}
						</label>
						<span id="gate-nickname-hint" class="text-xs text-cream-muted">
							{m.nickname_hint({ max: NICKNAME_MAX })}
						</span>
					</div>
					<input
						id="gate-nickname"
						name="nickname"
						type="text"
						bind:value={draft}
						oninput={onNicknameInput}
						onblur={() => (draft = profile.nickname)}
						onkeydown={(e) => e.key === 'Enter' && join()}
						placeholder={m.nickname_placeholder()}
						aria-describedby="gate-nickname-hint"
						autocomplete="off"
						autocapitalize="words"
						enterkeyhint="go"
						spellcheck="false"
						class="h-12 w-full rounded-xl bg-night px-4 text-lg font-bold text-cream ring-2 ring-night-3 placeholder:font-normal placeholder:text-cream-muted/70 focus:ring-banana focus:outline-none"
					/>
				</div>
			</div>
			<AvatarPicker avatar={profile.avatar} onchange={(next) => profile.setAvatar(next)} />
		</section>

		{#snippet footer()}
			{#if !canJoin}
				<p class="text-center text-sm text-cream-muted">{m.gate_need_nickname()}</p>
			{/if}
			<Button tone="strawberry" disabled={!canJoin || check.kind === 'loading'} onclick={join}>
				{m.gate_join()}
			</Button>
			<a href={resolve('/')} class="{homeLink} bg-night-3 text-cream">{m.net_home()}</a>
		{/snippet}
	</Screen>
{/if}
