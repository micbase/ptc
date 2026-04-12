<script setup lang="ts">
import { useRoute } from 'vue-router'

const route = useRoute()
</script>

<template>
  <div class="w-full">
    <!-- Tab navigation -->
    <div class="border-b border-gray-200 bg-white px-4">
      <nav class="flex gap-1 -mb-px">
        <router-link
          to="/rates"
          :class="[
            'px-4 py-3 text-sm font-medium border-b-2 transition-colors',
            route.path === '/rates'
              ? 'border-blue-600 text-blue-700'
              : 'border-transparent text-gray-500 hover:text-gray-700 hover:border-gray-300',
          ]"
        >
          Rate Comparison
        </router-link>
        <router-link
          to="/planner"
          :class="[
            'px-4 py-3 text-sm font-medium border-b-2 transition-colors',
            route.path === '/planner'
              ? 'border-blue-600 text-blue-700'
              : 'border-transparent text-gray-500 hover:text-gray-700 hover:border-gray-300',
          ]"
        >
          Switch Planner
        </router-link>
        <router-link
          to="/history"
          :class="[
            'px-4 py-3 text-sm font-medium border-b-2 transition-colors',
            route.path === '/history'
              ? 'border-blue-600 text-blue-700'
              : 'border-transparent text-gray-500 hover:text-gray-700 hover:border-gray-300',
          ]"
        >
          Switch History
        </router-link>
      </nav>
    </div>

    <router-view />
  </div>
</template>
