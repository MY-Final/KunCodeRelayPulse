import ChannelManagementPage from "@/pages/ChannelManagementPage"
import ModelStatusPage from "@/pages/ModelStatusPage"

export default function App() {
  if (["/admin/channels", "/admin/channels/", "/static/admin/channels", "/static/admin/channels/"].includes(window.location.pathname)) {
    return <ChannelManagementPage />
  }
  return <ModelStatusPage />
}
