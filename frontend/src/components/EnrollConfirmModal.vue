<script setup lang="ts">
import { ref, watch } from 'vue'
import { addSwitchEvent } from '../api'
import type { AddSwitchEventRequest } from '../types'

const props = defineProps<{
  show: boolean
  electricityRateId: number
  repCompany: string
  product: string
  termValue: number
  kwh1000Cents: number
  enrollUrl: string
  cancelFee?: string
  // Pre-suggested dates (can be overridden by user)
  suggestedSwitchDate?: string
  suggestedExpirationDate?: string
}>()

const emit = defineEmits<{
  close: []
  recorded: [id: number]
}>()

const switchDate = ref('')
const expirationDate = ref('')
const etfText = ref('')
const notes = ref('')
const saving = ref(false)
const error = ref('')

function computeExpiration(from: string): string {
  if (!from || !props.termValue || props.termValue <= 1) return ''
  const d = new Date(from)
  d.setMonth(d.getMonth() + props.termValue)
  return d.toISOString().slice(0, 10)
}

watch(() => props.show, (val) => {
  if (val) {
    const today = new Date().toISOString().slice(0, 10)
    switchDate.value = props.suggestedSwitchDate || today
    expirationDate.value = props.suggestedExpirationDate || computeExpiration(switchDate.value)
    etfText.value = props.cancelFee ?? ''
    notes.value = ''
    error.value = ''
  }
}, { immediate: true })

watch(switchDate, (val) => {
  expirationDate.value = computeExpiration(val)
})

async function onConfirm() {
  error.value = ''
  saving.value = true
  try {
    const req: AddSwitchEventRequest = {
      electricity_rate_id: props.electricityRateId,
      switch_date: switchDate.value,
      contract_expiration_date: expirationDate.value,
      etf_text: etfText.value,
      notes: notes.value,
    }
    const record = await addSwitchEvent(req)
    emit('recorded', record.id)
    // Open enroll URL in new tab
    if (props.enrollUrl) {
      window.open(props.enrollUrl, '_blank', 'noopener,noreferrer')
    }
    emit('close')
  } catch (e: any) {
    error.value = e.message ?? 'Failed to record switch'
  } finally {
    saving.value = false
  }
}

function onSkip() {
  // Open without recording
  if (props.enrollUrl) {
    window.open(props.enrollUrl, '_blank', 'noopener,noreferrer')
  }
  emit('close')
}
</script>

<template>
  <Teleport to="body">
    <div
      v-if="show"
      class="fixed inset-0 z-50 flex items-center justify-center bg-black/40"
      @click.self="emit('close')"
    >
      <div class="bg-white rounded-xl shadow-xl w-full max-w-md mx-4 p-6">
        <h2 class="text-lg font-semibold text-gray-900 mb-1">Record this switch?</h2>
        <p class="text-sm text-gray-500 mb-4">
          Log this enrollment in your switch history before visiting the provider's site.
        </p>

        <!-- Plan summary -->
        <div class="bg-gray-50 rounded-lg px-4 py-3 mb-4 text-sm">
          <div class="font-medium text-gray-800">{{ repCompany }}</div>
          <div class="text-gray-600">{{ product }}</div>
          <div class="text-gray-500 text-xs mt-0.5">
            {{ termValue === 1 ? 'Variable' : `${termValue}-month Fixed` }} &mdash;
            {{ kwh1000Cents.toFixed(2) }}¢/kWh at 1000 kWh
          </div>
        </div>

        <!-- Form fields -->
        <div class="space-y-3 mb-4">
          <div>
            <label class="block text-xs font-medium text-gray-600 mb-1">Switch Date</label>
            <input
              v-model="switchDate"
              type="date"
              required
              class="w-full border border-gray-300 rounded px-3 py-1.5 text-sm focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
          </div>
          <div>
            <label class="block text-xs font-medium text-gray-600 mb-1">New Contract Expiration</label>
            <input
              v-model="expirationDate"
              type="date"
              required
              class="w-full border border-gray-300 rounded px-3 py-1.5 text-sm focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
          </div>
          <div>
            <label class="block text-xs font-medium text-gray-600 mb-1">Early Termination Fee (optional)</label>
            <input
              v-model="etfText"
              type="text"
              placeholder="e.g. 20, 20/remaining month"
              class="w-full border border-gray-300 rounded px-3 py-1.5 text-sm focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
          </div>
          <div>
            <label class="block text-xs font-medium text-gray-600 mb-1">Notes (optional)</label>
            <input
              v-model="notes"
              type="text"
              placeholder="e.g. Switched from TXU"
              class="w-full border border-gray-300 rounded px-3 py-1.5 text-sm focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
          </div>
        </div>

        <div v-if="error" class="mb-3 text-sm text-red-700 bg-red-50 border border-red-200 rounded px-3 py-2">
          {{ error }}
        </div>

        <div class="flex gap-2 justify-end">
          <button
            @click="onSkip"
            class="px-4 py-1.5 text-sm text-gray-600 hover:text-gray-900 border border-gray-300 rounded hover:bg-gray-50"
          >
            Skip &amp; Enroll
          </button>
          <button
            @click="onConfirm"
            :disabled="saving || !switchDate || !expirationDate"
            class="px-4 py-1.5 text-sm font-medium rounded bg-blue-600 text-white hover:bg-blue-700 disabled:opacity-50 disabled:cursor-not-allowed"
          >
            {{ saving ? 'Saving…' : 'Record &amp; Enroll' }}
          </button>
        </div>
      </div>
    </div>
  </Teleport>
</template>
