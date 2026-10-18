<script lang="ts">
	import AvatarBadge from '$lib/components/AvatarBadge.svelte';
	import AvatarPicker from '$lib/components/AvatarPicker.svelte';
	import LanguageSwitch from '$lib/components/LanguageSwitch.svelte';
	import RoomCodeInput from '$lib/components/RoomCodeInput.svelte';
	import Wordmark from '$lib/components/Wordmark.svelte';
	import { capNickname, NICKNAME_MAX } from '$lib/nickname';
	import { m } from '$lib/paraglide/messages';
	import { profile } from '$lib/profile.svelte';
	import { isCompleteRoomCode } from '$lib/roomCode';
	import { toasts } from '$lib/toast.svelte';

	let draft = $state(profile.nickname);
	let code = $state('');

	const canCreate = $derived(profile.nickname.length > 0);
	const canJoin = $derived(canCreate && isCompleteRoomCode(code));

	function onNicknameInput() {
		draft = capNickname(draft.trimStart());
		profile.setNickname(draft);
	}

	function comingSoon() {
		toasts.show(m.coming_soon());
	}

	const primary =
		'h-14 w-full rounded-2xl px-6 text-xl font-black text-night shadow-pop transition active:translate-y-1 active:shadow-none disabled:bg-night-3 disabled:text-cream-muted disabled:shadow-none';
</script>

<!--
	Mobile-first, thumb-friendly: everything needed to start a game fits in one
	screen (390x844 and 360x740, no scrolling). The identity (logo, nickname,
	avatar) sits on top and the actions are anchored to the bottom.
-->
<main
	class="mx-auto flex min-h-dvh w-full max-w-md flex-col gap-3 px-3 pt-[max(0.5rem,env(safe-area-inset-top))] pb-[max(0.75rem,env(safe-area-inset-bottom))] tall:gap-4"
>
	<header class="flex flex-col items-center gap-1 text-center">
		<div class="flex w-full justify-end">
			<LanguageSwitch />
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
					type="text"
					bind:value={draft}
					oninput={onNicknameInput}
					onblur={() => (draft = profile.nickname)}
					placeholder={m.nickname_placeholder()}
					aria-describedby="nickname-hint"
					autocomplete="nickname"
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
		<button type="button" disabled={!canCreate} onclick={comingSoon} class="{primary} bg-banana">
			{m.create_game()}
		</button>

		<div class="flex items-center gap-3 text-sm text-cream-muted" aria-hidden="true">
			<span class="h-px flex-1 bg-night-3"></span>
			<span class="font-bold uppercase">{m.join_divider()}</span>
			<span class="h-px flex-1 bg-night-3"></span>
		</div>

		<div class="flex flex-col gap-1">
			<div class="flex flex-wrap items-baseline justify-between gap-x-2 px-1" aria-hidden="true">
				<span class="text-sm font-bold text-cream-muted">{m.code_label()}</span>
				<span id="code-hint" class="text-xs text-cream-muted">{m.code_hint()}</span>
			</div>
			<RoomCodeInput bind:value={code} describedby="code-hint" />
		</div>

		<button type="button" disabled={!canJoin} onclick={comingSoon} class="{primary} bg-strawberry">
			{m.join_game()}
		</button>

		<a
			href="/api/auth/google/login?returnTo=%2F"
			rel="external"
			onclick={(event) => {
				event.preventDefault();
				comingSoon();
			}}
			class="flex h-12 items-center justify-center rounded-2xl px-4 font-bold text-cream underline decoration-2 underline-offset-4 ring-1 ring-night-3"
		>
			{m.sign_in_google()}
		</a>
	</section>
</main>
