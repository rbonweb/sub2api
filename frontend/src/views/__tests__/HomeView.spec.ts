import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'

import HomeView from '../HomeView.vue'

describe('HomeView placeholder', () => {
  beforeEach(() => {
    localStorage.clear()
    document.documentElement.classList.remove('dark')
    vi.spyOn(window, 'matchMedia').mockReturnValue({ matches: false } as MediaQueryList)
  })

  it('renders only the AI Gateway placeholder', () => {
    const wrapper = mount(HomeView)

    expect(wrapper.get('[data-testid="home-placeholder"]').text()).toBe('AI Gateway')
  })

  it('does not render any links, buttons or embedded content', () => {
    const wrapper = mount(HomeView)

    expect(wrapper.findAll('a').length).toBe(0)
    expect(wrapper.findAll('button').length).toBe(0)
    expect(wrapper.find('iframe').exists()).toBe(false)
    expect(wrapper.text()).not.toMatch(/dashboard|login/i)
  })

  it('applies the saved dark theme', () => {
    localStorage.setItem('theme', 'dark')

    mount(HomeView)

    expect(document.documentElement.classList.contains('dark')).toBe(true)
  })
})
