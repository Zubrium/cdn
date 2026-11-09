import { Outlet } from "react-router-dom";

export default function SnapshotManagementLayout() {
    return (
        <div className="page-grid">
            <Outlet />
        </div>
    );
}
