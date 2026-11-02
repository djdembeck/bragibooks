<script lang="ts">
	import Modal from '../Modal.svelte';

	let open = $state(true);

	export function setOpen(next: boolean) {
		open = next;
	}
</script>

<Modal bind:open title="Remove sources">
	<p>Choose sources to remove.</p>
</Modal>
<p id="probe-bound">{open ? 'open' : 'closed'}</p>
