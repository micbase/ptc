<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { fetchSwitchEvents, addSwitchEvent, fetchPlans, fetchLatestDate } from '../api'
import type { SwitchRecord, ElectricityRate, AddSwitchEventRequest } from '../types'

const records = ref<SwitchRecord[]>([])
const loading = ref(false)
const error = ref('')

// ── Add modal state ───────────────────────────────────────────────────────────
const showAddModal = ref(false)
const addError = ref('')
const addSaving = ref(false)

// Plan picker
const pickerDate = ref('')
const pickerPlans = ref<ElectricityRate[]>([])
const pickerLoading = ref(false)
const pickerSearch = ref('')
const selectedPlan = ref<ElectricityRate | null>(null)

// Form fields
const switchDate = ref('')
const expirationDate = ref('')
const addNotes = ref('')

async function load() {
  loading.value = true
  error.value = ''
  try {
    records.value = await fetchSwitchEvents()
  } catch (e: any) {
    error.value = e.message ?? 'Failed to load switch history'
  } finally {
    loading.value = false
  }
}

onMounted(load)

// ── Plan picker ───────────────────────────────────────────────────────────────
async function openAddModal() {
  addError.value = ''
  selectedPlan.value = null
  pickerSearch.value = ''
  pickerPlans.value = []
  switchDate.value = new Date().toISOString().slice(0, 10)
  expirationDate.value = ''
  addNotes.value = ''
  showAddModal.value = true

  // Load latest date and plans
  try {
    pickerLoading.value = true
    const latest = await fetchLatestDate()
    pickerDate.value = latest
    pickerPlans.value = await fetchPlans(latest)
  } catch (e: any) {
    addError.value = e.message ?? 'Failed to load plans'
  } finally {
    pickerLoading.value = false
  }
}

async function onPickerDateChange() {
  if (!pickerDate.value) return
  pickerLoading.value = true
  selectedPlan.value = null
  try {
    pickerPlans.value = await fetchPlans(pickerDate.value)
  } catch (e: any) {
    addError.value = e.message ?? 'Failed to load plans'
  } finally {
    pickerLoading.value = false
  }
}

const filteredPlans = () => {
  const q = pickerSearch.value.toLowerCase()
  if (!q) return pickerPlans.value
  return pickerPlans.value.filter(
    (p) =>
      (p.rep_company ?? '').toLowerCase().includes(q) ||
      (p.product ?? '').toLowerCase().includes(q),
  )
}

function selectPlan(plan: ElectricityRate) {
  selectedPlan.value = plan
  recalcExpiration()
}

function recalcExpiration() {
  const plan = selectedPlan.value
  if (plan && plan.term_value && plan.term_value > 1 && switchDate.value) {
    const d = new Date(switchDate.value)
    d.setMonth(d.getMonth() + plan.term_value)
    expirationDate.value = d.toISOString().slice(0, 10)
  }
}

async function onSubmitAdd() {
  if (!selectedPlan.value) return
  addError.value = ''
  addSaving.value = true
  try {
    const req: AddSwitchEventRequest = {
      electricity_rate_id: selectedPlan.value.id,
      switch_date: switchDate.value,
      contract_expiration_date: expirationDate.value,
      notes: addNotes.value,
    }
    await addSwitchEvent(req)
    showAddModal.value = false
    await load()
  } catch (e: any) {
    addError.value = e.message ?? 'Failed to add switch event'
  } finally {
    addSaving.value = false
  }
}

function formatDate(s: string) {
  return s ? s.slice(0, 10) : '—'
}
</script>

<template>
  <div class="w-full px-4 py-6">
    <div class="flex items-center justify-between mb-6">
      <h1 class="text-2xl font-bold text-gray-900">Switch History</h1>
      <button
        @click="openAddModal"
        class="px-4 py-2 text-sm font-medium rounded bg-blue-600 text-white hover:bg-blue-700"
      >
        + Add Switch
      </button>
    </div>

    <div v-if="error" class="mb-4 text-sm text-red-700 bg-red-50 border border-red-200 rounded px-3 py-2">
      {{ error }}
    </div>

    <div v-if="loading" class="text-sm text-gray-500">Loading…</div>

    <div v-else-if="records.length === 0" class="bg-white rounded-lg shadow p-8 text-center text-gray-400 text-sm">
      No switch events recorded yet. Click <strong>+ Add Switch</strong> to log one manually, or enroll in a plan to record it automatically.
    </div>

    <div v-else class="bg-white rounded-lg shadow overflow-hidden">
      <div class="overflow-x-auto">
        <table class="w-full text-sm">
          <thead>
            <tr class="bg-gray-50 text-gray-600 text-xs uppercase">
              <th class="text-left px-4 py-3 font-medium">Switch Date</th>
              <th class="text-left px-4 py-3 font-medium">Contract Expires</th>
              <th class="text-left px-4 py-3 font-medium">Provider</th>
              <th class="text-left px-4 py-3 font-medium">Plan</th>
              <th class="text-center px-4 py-3 font-medium">Term</th>
              <th class="text-right px-4 py-3 font-medium">¢/kWh@1000</th>
              <th class="text-left px-4 py-3 font-medium">ETF</th>
              <th class="text-left px-4 py-3 font-medium">Notes</th>
            </tr>
          </thead>
          <tbody>
            <tr
              v-for="r in records"
              :key="r.id"
              class="border-t border-gray-100 hover:bg-gray-50"
            >
              <td class="px-4 py-3 tabular-nums whitespace-nowrap text-gray-700">{{ formatDate(r.switch_date) }}</td>
              <td class="px-4 py-3 tabular-nums whitespace-nowrap text-gray-700">{{ formatDate(r.contract_expiration_date) }}</td>
              <td class="px-4 py-3 text-gray-800 font-medium">{{ r.rep_company }}</td>
              <td class="px-4 py-3 text-gray-600 max-w-xs truncate">{{ r.product }}</td>
              <td class="px-4 py-3 text-center text-gray-600 whitespace-nowrap">
                {{ r.term_value === 1 ? 'Variable' : `${r.term_value}m Fixed` }}
              </td>
              <td class="px-4 py-3 text-right tabular-nums text-gray-700">{{ (r.kwh1000 * 100).toFixed(2) }}</td>
              <td class="px-4 py-3 text-gray-600 text-xs whitespace-nowrap">{{ r.cancel_fee || '—' }}</td>
              <td class="px-4 py-3 text-gray-500 text-xs max-w-xs truncate">{{ r.notes || '—' }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

    <!-- Add Switch Modal -->
    <Teleport to="body">
      <div
        v-if="showAddModal"
        class="fixed inset-0 z-50 flex items-center justify-center bg-black/40"
        @click.self="showAddModal = false"
      >
        <div class="bg-white rounded-xl shadow-xl w-full max-w-lg mx-4 p-6 max-h-[90vh] flex flex-col">
          <h2 class="text-lg font-semibold text-gray-900 mb-4">Add Switch Event</h2>

          <div class="overflow-y-auto flex-1 space-y-4 pr-1">
            <!-- Plan Picker -->
            <div>
              <label class="block text-xs font-medium text-gray-600 mb-1">Plans for Date</label>
              <input
                v-model="pickerDate"
                type="date"
                @change="onPickerDateChange"
                class="w-full border border-gray-300 rounded px-3 py-1.5 text-sm focus:outline-none focus:ring-1 focus:ring-blue-500 mb-2"
              />
              <input
                v-model="pickerSearch"
                type="text"
                placeholder="Search provider or plan…"
                class="w-full border border-gray-300 rounded px-3 py-1.5 text-sm focus:outline-none focus:ring-1 focus:ring-blue-500 mb-2"
              />
              <div v-if="pickerLoading" class="text-xs text-gray-500 py-2">Loading plans…</div>
              <div v-else class="border border-gray-200 rounded max-h-48 overflow-y-auto">
                <div
                  v-for="p in filteredPlans()"
                  :key="p.id"
                  @click="selectPlan(p)"
                  :class="[
                    'px-3 py-2 text-sm cursor-pointer hover:bg-blue-50',
                    selectedPlan?.id === p.id ? 'bg-blue-100 border-l-2 border-blue-600' : '',
                  ]"
                >
                  <div class="font-medium text-gray-800">{{ p.rep_company }}</div>
                  <div class="text-gray-600 text-xs truncate">
                    {{ p.product }} &mdash;
                    {{ p.term_value === 1 ? 'Variable' : `${p.term_value}m Fixed` }} &mdash;
                    {{ p.kwh1000?.toFixed(2) }}¢/kWh
                  </div>
                </div>
                <div v-if="filteredPlans().length === 0 && !pickerLoading" class="px-3 py-3 text-xs text-gray-400">
                  No plans found.
                </div>
              </div>
            </div>

            <!-- Dates -->
            <div class="grid grid-cols-2 gap-3">
              <div>
                <label class="block text-xs font-medium text-gray-600 mb-1">Switch Date</label>
                <input
                  v-model="switchDate"
                  type="date"
                  required
                  @change="recalcExpiration"
                  class="w-full border border-gray-300 rounded px-3 py-1.5 text-sm focus:outline-none focus:ring-1 focus:ring-blue-500"
                />
              </div>
              <div>
                <label class="block text-xs font-medium text-gray-600 mb-1">Contract Expiration</label>
                <input
                  v-model="expirationDate"
                  type="date"
                  required
                  class="w-full border border-gray-300 rounded px-3 py-1.5 text-sm focus:outline-none focus:ring-1 focus:ring-blue-500"
                />
              </div>
            </div>

            <!-- Notes -->
            <div>
              <label class="block text-xs font-medium text-gray-600 mb-1">Notes (optional)</label>
              <input
                v-model="addNotes"
                type="text"
                placeholder="e.g. Switched from TXU"
                class="w-full border border-gray-300 rounded px-3 py-1.5 text-sm focus:outline-none focus:ring-1 focus:ring-blue-500"
              />
            </div>

            <div v-if="addError" class="text-sm text-red-700 bg-red-50 border border-red-200 rounded px-3 py-2">
              {{ addError }}
            </div>
          </div>

          <div class="flex gap-2 justify-end pt-4 border-t border-gray-100 mt-4">
            <button
              @click="showAddModal = false"
              class="px-4 py-1.5 text-sm text-gray-600 hover:text-gray-900 border border-gray-300 rounded hover:bg-gray-50"
            >
              Cancel
            </button>
            <button
              @click="onSubmitAdd"
              :disabled="addSaving || !selectedPlan || !switchDate || !expirationDate"
              class="px-4 py-1.5 text-sm font-medium rounded bg-blue-600 text-white hover:bg-blue-700 disabled:opacity-50 disabled:cursor-not-allowed"
            >
              {{ addSaving ? 'Saving…' : 'Save Switch' }}
            </button>
          </div>
        </div>
      </div>
    </Teleport>
  </div>
</template>
