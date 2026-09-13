import ChannelManagementPage from "@/pages/ChannelManagementPage"
import ModelStatusPage from "@/pages/ModelStatusPage"
import ProbeSettingsPage from "@/pages/ProbeSettingsPage"

export default function App() {
  if (["/admin/channels", "/admin/channels/", "/static/admin/channels", "/static/admin/channels/"].includes(window.location.pathname)) {
    return <ChannelManagementPage />
  }
  if (["/admin/settings", "/admin/settings/", "/static/admin/settings", "/static/admin/settings/"].includes(window.location.pathname)) {
    return <ProbeSettingsPage />
  }
  return <ModelStatusPage />
}
