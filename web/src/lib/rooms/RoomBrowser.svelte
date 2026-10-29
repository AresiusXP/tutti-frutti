<script lang="ts">
	import { onMount, untrack } from 'svelte';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { ApiError, api } from '$lib/api';
	import Button from '$lib/components/Button.svelte';
	import LanguageSwitch from '$lib/components/LanguageSwitch.svelte';
	import Screen from '$lib/components/Screen.svelte';
	import { errorMessage } from '$lib/errors';
	import { clearRoomMark, markRoom } from '$lib/game/roomMark';
	import { i18n } from '$lib/i18n.svelte';
	import { m } from '$lib/paraglide/messages';
	import { profile } from '$lib/profile.svelte';
	import { toasts } from '$lib/toast.svelte';
	import { RoomBrowser } from './browser.svelte';

	const browser = new RoomBrowser(untrack(() => i18n.locale));
	let joining = $state<string | null>(null);
	let joinRequest: AbortController | null = null;
	onMount(() => {
		const stop = browser.start(document);
		return () => {
			stop();
			joinRequest?.abort();
		};
	});

	async function join(code: string) {
		if (joining) return;
		joining = code;
		const request = new AbortController();
		joinRequest = request;
		try {
			const info = await api.getRoom(code, { signal: request.signal });
			if (request.signal.aborted) return;
			if (!info.joinable) {
				toasts.show(
					info.phase === 'final'
						? m.home_game_over()
						: info.players >= info.maxPlayers
							? m.home_room_full()
							: m.home_not_joinable()
				);
				void browser.refresh();
				return;
			}
			// With no nickname, the room route opens its existing profile gate.
			if (profile.nickname) markRoom(info.code);
			else clearRoomMark(info.code);
			await goto(resolve('/[code=roomcode]', { code: info.code }));
		} catch (error) {
			if (!request.signal.aborted)
				toasts.show(errorMessage(error instanceof ApiError ? error.code : 'unknown'));
		} finally {
			if (joinRequest === request) {
				joining = null;
				joinRequest = null;
			}
		}
	}
</script>

<Screen label={m.rooms_title()}>
	{#snippet header()}
		<div class="flex items-center justify-between gap-2">
			<h1 class="text-2xl font-black">{m.rooms_title()}</h1>
			<LanguageSwitch />
		</div>
		<p class="text-sm text-cream-muted">{m.rooms_casual()}</p>
	{/snippet}
	<div role="group" aria-label={m.rooms_language()} class="grid grid-cols-4 gap-1">
		{#each ['es', 'en', 'nl', undefined] as lang (lang)}
			<button
				type="button"
				aria-pressed={browser.lang === lang}
				onclick={() => browser.filter(lang as 'es' | 'en' | 'nl' | undefined)}
				class="h-12 rounded-xl font-bold {browser.lang === lang
					? 'bg-banana text-night'
					: 'bg-night-3 text-cream'}"
			>
				{lang?.toUpperCase() ?? m.rooms_all()}
			</button>
		{/each}
	</div>
	<Button tone="plain" size="md" disabled={browser.loading} onclick={() => browser.refresh()}
		>{m.rooms_refresh()}</Button
	>
	<div role="status" aria-live="polite">
		{#if browser.loading}<p class="text-center text-cream-muted">{m.rooms_loading()}</p>{/if}
		{#if browser.error}<p class="text-center text-strawberry">{errorMessage(browser.error)}</p>{/if}
	</div>
	{#if !browser.loading && !browser.error && browser.rooms.length === 0}
		<div class="flex flex-col gap-4 rounded-2xl bg-night-2 p-4 text-center">
			<p>{m.rooms_empty()}</p>
			<a
				href={resolve('/?visibility=public')}
				class="flex min-h-12 items-center justify-center rounded-xl bg-banana px-3 font-bold text-night"
				>{m.rooms_create()}</a
			>
		</div>
	{/if}
	<ul class="flex flex-col gap-3" aria-label={m.rooms_title()}>
		{#each browser.rooms as room (room.code)}
			<li class="flex flex-col gap-2 rounded-2xl bg-night-2 p-3">
				<div class="flex items-center justify-between gap-2 font-bold">
					<h2 class="text-xl text-banana">
						{room.code} <span class="text-sm text-cream">{room.lang.toUpperCase()}</span>
					</h2>
					<span>{m.rooms_players({ count: room.players, max: room.maxPlayers })}</span>
				</div>
				<p class="line-clamp-2 text-sm text-cream-muted">{room.categoryLabels.join(' · ')}</p>
				<p class="text-sm text-cream-muted">
					{room.rounds > 0 ? m.rooms_rounds({ count: room.rounds }) : m.rooms_unlimited()} · {room.timeLimitSeconds >
					0
						? m.rooms_time({ seconds: room.timeLimitSeconds })
						: m.rooms_no_timer()}
				</p>
				<Button
					size="md"
					disabled={joining !== null}
					aria-busy={joining === room.code}
					onclick={() => join(room.code)}>{m.rooms_join({ code: room.code })}</Button
				>
			</li>
		{/each}
	</ul>
	{#snippet footer()}
		<a
			href={resolve('/')}
			class="flex h-12 items-center justify-center rounded-2xl bg-night-3 font-bold"
			>{m.net_home()}</a
		>
	{/snippet}
</Screen>
