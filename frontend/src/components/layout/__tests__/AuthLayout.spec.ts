import { mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import AuthLayout from '../AuthLayout.vue'

const { appStore } = vi.hoisted(() => ({
  appStore: {
    siteName: 'Test site',
    siteLogo: '',
    cachedPublicSettings: { site_subtitle: 'Test subtitle' } as Record<string, unknown>,
    publicSettingsLoaded: true,
    fetchPublicSettings: vi.fn()
  }
}))

vi.mock('@/stores', () => ({
  useAppStore: () => appStore
}))

function mountLayout(props: Record<string, unknown> = {}) {
  return mount(AuthLayout, {
    props,
    slots: {
      default: '<form data-testid="slot-content" />',
      footer: '<p data-testid="slot-footer">footer</p>'
    }
  })
}

describe('AuthLayout', () => {
  beforeEach(() => {
    appStore.fetchPublicSettings.mockClear()
  })

  it('shows the logo, site name, subtitle and copyright by default', () => {
    const wrapper = mountLayout()

    expect(wrapper.find('img[alt="Logo"]').exists()).toBe(true)
    expect(wrapper.get('h1').text()).toBe('Test site')
    expect(wrapper.text()).toContain('Test subtitle')
    expect(wrapper.text()).toContain('All rights reserved.')
  })

  it('renders only the slot content in minimal mode', () => {
    const wrapper = mountLayout({ minimal: true })

    expect(wrapper.find('[data-testid="slot-content"]').exists()).toBe(true)
    expect(wrapper.find('img[alt="Logo"]').exists()).toBe(false)
    expect(wrapper.find('h1').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('Test site')
    expect(wrapper.text()).not.toContain('Test subtitle')
    expect(wrapper.text()).not.toContain('All rights reserved.')
  })
})
